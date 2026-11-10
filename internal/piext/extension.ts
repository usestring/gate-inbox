// Gate Inbox's pi extension. The board writes this file under its hooks dir
// with a content-stamped name and loads it into every pi it launches with
// `pi -e <path>`; nothing is written to ~/.pi.
//
// It is deliberately thin. It reports pi's lifecycle to
// `$GATE_INBOX_BIN hook pi <event>` and every decision about what that means
// (which status, when to remove the status file, later which tool calls run)
// is made there, in Go, beside the code Claude Code's hooks already reach.
//
// Outside a session the board launched (no GATE_INBOX_BIN or no status file
// in the environment) it registers nothing and pi runs exactly as it would
// without it.

import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const bin = process.env.GATE_INBOX_BIN ?? "";
const statusFile = process.env.GATE_INBOX_STATUS_FILE ?? "";

// hookTimeoutMs bounds one hook call. A hook that hangs must never hold pi
// up: the call is killed and answers undefined, as a failed one does.
const hookTimeoutMs = 5000;

// runHook runs `$GATE_INBOX_BIN hook pi <verb>` with payload as JSON on
// stdin and answers its stdout parsed as JSON. Every failure (no binary, a
// non-zero exit, a timeout, output that is not JSON) answers undefined, so a
// caller treats it as "the board said nothing". A gate built on it (the
// PreToolUse call) must read undefined as its fail-closed answer.
function runHook(verb: string, payload: unknown, timeoutMs = hookTimeoutMs): Promise<unknown> {
  return new Promise((resolve) => {
    if (!bin) {
      resolve(undefined);
      return;
    }
    let settled = false;
    const done = (value: unknown) => {
      if (!settled) {
        settled = true;
        resolve(value);
      }
    };
    let child;
    try {
      child = spawn(bin, ["hook", "pi", verb], { stdio: ["pipe", "pipe", "ignore"] });
    } catch {
      done(undefined);
      return;
    }
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      done(undefined);
    }, timeoutMs);
    let out = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      out += chunk;
    });
    child.on("error", () => {
      clearTimeout(timer);
      done(undefined);
    });
    child.on("close", (code: number | null) => {
      clearTimeout(timer);
      if (code !== 0 || out.trim() === "") {
        done(undefined);
        return;
      }
      try {
        done(JSON.parse(out));
      } catch {
        done(undefined);
      }
    });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(payload ?? {}));
  });
}

// Status events run one at a time, in the order pi raised them: two hook
// processes racing on the status file could log agent_settled before the
// agent_start it follows. The queue also lets session_shutdown wait for
// every event before it, so the file is gone before pi exits.
let queue: Promise<unknown> = Promise.resolve();

function report(event: string, fields: Record<string, unknown> = {}): Promise<unknown> {
  queue = queue.then(() => runHook(event, { event, ...fields })).catch(() => undefined);
  return queue;
}

// endedInError reads an agent_end's messages for the assistant's last stop
// reason. Only an error is reported; a retry that follows starts the run
// again and the next agent_start says so.
function endedInError(messages: unknown): boolean {
  if (!Array.isArray(messages)) return false;
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i] as { role?: string; stopReason?: string } | undefined;
    if (message?.role === "assistant") return message.stopReason === "error";
  }
  return false;
}

export default function gateInbox(pi: ExtensionAPI) {
  if (!bin || !statusFile) return;

  pi.on("session_start", async (event, ctx) => {
    await report("session_start", { reason: event.reason, cwd: ctx.cwd, session_id: ctx.sessionManager.getSessionId() });
  });
  pi.on("agent_start", async () => {
    await report("agent_start");
  });
  pi.on("agent_end", async (event) => {
    await report("agent_end", { errored: endedInError(event.messages) });
  });
  pi.on("agent_settled", async (event) => {
    await report("agent_settled", { aborted: event.aborted === true });
  });
  // pi does not await these before drawing or closing the dialog, so the
  // queue, not the handler, keeps them in order.
  pi.on("ui_prompt_start", async (event) => {
    await report("ui_prompt_start", { kind: event.kind });
  });
  pi.on("ui_prompt_end", async (_event, ctx) => {
    await report("ui_prompt_end", { idle: ctx.isIdle() });
  });
  pi.on("session_shutdown", async (event, ctx) => {
    await report("session_shutdown", { reason: event.reason, cwd: ctx.cwd });
  });
  registerToolGate(pi);
}

// gateTimeoutMs bounds one PreToolUse decision. It is longer than a status
// event's: the merge gate asks GitHub, and the answer is worth waiting for.
const gateTimeoutMs = 30000;

type GateDecision = {
  permissionDecision: "allow" | "deny" | "ask";
  reason?: string;
  tool?: string;
  ruleSuggestion?: string;
};

// decide asks `hook pi PreToolUse` about one call. Anything but a
// well-formed answer, including no answer at all, is "ask": the gate fails
// closed, never open.
async function decide(payload: Record<string, unknown>): Promise<GateDecision> {
  const answer = (await runHook("PreToolUse", payload, gateTimeoutMs)) as Partial<GateDecision> | undefined;
  const verdict = answer?.permissionDecision;
  if (verdict === "allow" || verdict === "deny" || verdict === "ask") {
    return answer as GateDecision;
  }
  return { permissionDecision: "ask", reason: "Gate Inbox did not answer" };
}

// registerToolGate holds every tool call until the board decides it. The
// decision is the board's, in Go; this only carries the call there and the
// answer back.
function registerToolGate(pi: ExtensionAPI) {
  pi.on("tool_call", async (event, ctx) => {
    const tool = pi.getAllTools().find((t) => t.name === event.toolName);
    const d = await decide({
      tool_name: event.toolName,
      tool_input: event.input,
      cwd: ctx.cwd,
      tool_use_id: event.toolCallId,
      read_only: tool?.annotations?.readOnlyHint === true,
    });
    if (ctx.signal?.aborted) return { block: true, reason: "cancelled" };
    if (d.permissionDecision === "allow") return undefined;
    if (d.permissionDecision === "deny") {
      return { block: true, reason: `Gate Inbox refused this call: ${d.reason ?? "denied"}` };
    }
    return askUser(d);
  });
}

// askUser is what an "ask" does. Until the board can draw a permission
// dialog in the pane, a call that needs approval is refused with why.
async function askUser(d: GateDecision): Promise<{ block: true; reason: string }> {
  return { block: true, reason: `This call needs approval, which Gate Inbox cannot ask for yet: ${d.reason ?? "no rule allows it"}` };
}

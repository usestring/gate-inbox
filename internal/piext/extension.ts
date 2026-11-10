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
  title?: string;
  block?: string;
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
      ...userMessages(ctx.sessionManager.getBranch()),
    });
    if (ctx.signal?.aborted) return { block: true, reason: "cancelled" };
    if (d.permissionDecision === "allow") return undefined;
    if (d.permissionDecision === "deny") {
      return { block: true, reason: `Gate Inbox refused this call: ${d.reason ?? "denied"}` };
    }
    return askUser(d, event.toolCallId, ctx);
  });
}

// userMessages is the session's first user message and its latest three,
// which auto mode shows the classifier as what the user asked for. The board
// leaves out any a session or the board itself wrote.
function userMessages(entries: unknown[]): { brief?: string; recent?: string[] } {
  const texts: string[] = [];
  for (const entry of entries as { type?: string; message?: { role?: string; content?: unknown } }[]) {
    if (entry?.type !== "message" || entry.message?.role !== "user") continue;
    const content = entry.message.content;
    const text =
      typeof content === "string"
        ? content
        : Array.isArray(content)
          ? content.filter((p) => p?.type === "text").map((p) => String(p.text ?? "")).join("\n")
          : "";
    if (text.trim()) texts.push(text.slice(0, 4000));
  }
  return texts.length ? { brief: texts[0], recent: texts.slice(-3) } : {};
}

type Answer = { choice: "yes" | "always" | "no" | "cancel"; reason?: string };

// askUser puts a call the board asks about to the person at the pane, in a
// dialog drawn the way Claude Code draws its permission prompt, so the
// board reads it as one (guarded: never answered on the board's own say).
// Before it draws, the call is recorded whole, so a command the pane wraps
// still reads back exactly on the board.
async function askUser(
  d: GateDecision,
  toolUseId: string,
  ctx: { hasUI: boolean; cwd: string; signal?: AbortSignal; ui: { custom: Function } },
): Promise<{ block: true; reason: string } | undefined> {
  const title = d.title || d.tool || "Tool use";
  const block = d.block || "";
  if (!ctx.hasUI) {
    return { block: true, reason: `This call needs approval and this session has no screen to ask on: ${d.reason ?? ""}` };
  }
  await runHook("permission-pending", {
    tool: d.tool,
    title,
    block,
    cwd: ctx.cwd,
    reason: d.reason,
    ruleSuggestion: d.ruleSuggestion,
    tool_use_id: toolUseId,
  });
  let answer: Answer = { choice: "cancel" };
  try {
    answer = (await ctx.ui.custom(
      (_tui: unknown, _theme: unknown, keybindings: Keybindings, done: (a: Answer) => void) =>
        permissionDialog({ title, block, reason: d.reason, suggestion: d.ruleSuggestion }, keybindings, done, ctx.signal),
      { blocked: { kind: "permission", message: title } },
    )) as Answer;
  } finally {
    if (answer?.choice === "always") await runHook("grant-session", { tool_use_id: toolUseId });
    await runHook("permission-resolved", {});
  }
  switch (answer?.choice) {
    case "yes":
    case "always":
      return undefined;
    case "no":
      return {
        block: true,
        reason: answer.reason
          ? `The user refused this call and said: ${answer.reason}`
          : "The user refused this call. Ask them what to do differently before trying something else.",
      };
  }
  return { block: true, reason: "The user cancelled this call. Wait for their instructions." };
}

type Keybindings = { matches(data: string, id: string): boolean };

type DialogSpec = { title: string; block: string; reason?: string; suggestion?: string };

// The dialog's legend. "Enter to confirm" with "Esc to cancel" is the
// permission-prompt legend the board's readers know (dialog.approvalLegend).
const permissionLegend = "Enter to confirm · Esc to cancel";

// permissionDialog is the dialog's component: Up/Down and Enter, a choice's
// digit, or Esc. On the "No" choice, typing writes what the person wants
// instead, which goes back to the model.
function permissionDialog(spec: DialogSpec, keys: Keybindings, done: (a: Answer) => void, signal?: AbortSignal) {
  const choices: { label: string; choice: Answer["choice"] }[] = [{ label: "Yes", choice: "yes" }];
  if (spec.suggestion) choices.push({ label: `Yes, and don't ask again for: ${spec.suggestion}`, choice: "always" });
  choices.push({ label: "No, and tell pi what to do differently", choice: "no" });
  let cursor = 0;
  let reason = "";
  let finished = false;
  const finish = (a: Answer) => {
    if (!finished) {
      finished = true;
      done(a);
    }
  };
  signal?.addEventListener("abort", () => finish({ choice: "cancel" }), { once: true });
  const pick = (i: number) => finish({ choice: choices[i].choice, reason: choices[i].choice === "no" ? reason.trim() : undefined });
  return {
    render(width: number): string[] {
      return renderPermission(spec, choices.map((c) => c.label), cursor, reason, width);
    },
    handleInput(data: string): void {
      const onNo = choices[cursor].choice === "no";
      if (keys.matches(data, "tui.select.cancel")) finish({ choice: "cancel" });
      else if (keys.matches(data, "tui.select.up")) cursor = Math.max(0, cursor - 1);
      else if (keys.matches(data, "tui.select.down")) cursor = Math.min(choices.length - 1, cursor + 1);
      else if (keys.matches(data, "tui.select.confirm")) pick(cursor);
      else if (onNo && (data === "\x7f" || data === "\b")) reason = reason.slice(0, -1);
      else if (!(onNo && reason) && /^[1-9]$/.test(data) && Number(data) <= choices.length) pick(Number(data) - 1);
      else if (onNo && data.length === 1 && data >= " ") reason += data;
    },
    invalidate() {},
  };
}

// chunk cuts text into pieces of at most width characters.
function chunk(text: string, width: number): string[] {
  const chars = Array.from(text);
  if (chars.length === 0) return [""];
  const out: string[] = [];
  for (let i = 0; i < chars.length; i += width) out.push(chars.slice(i, i + width).join(""));
  return out;
}

// renderPermission draws the dialog in Claude Code's shape: a rule as its top
// edge, the title, the call behind a gutter, why the board asks, the
// question, numbered choices with the marker, and the legend. Every line fits
// width; the gutter block wraps rather than truncates, so the whole command
// is on screen.
function renderPermission(spec: DialogSpec, labels: string[], cursor: number, reason: string, width: number): string[] {
  const w = Math.max(20, width - 1);
  const lines = ["─".repeat(w), ` ${spec.title}`, ""];
  for (const row of (spec.block || "").split("\n")) {
    for (const part of chunk(row, w - 4)) lines.push(` │ ${part}`);
  }
  lines.push("");
  if (spec.reason) for (const part of chunk(`Gate Inbox asks because ${spec.reason}`, w - 1)) lines.push(` ${part}`);
  lines.push(" Do you want to proceed?");
  labels.forEach((label, i) => {
    const head = `${i === cursor ? " ❯ " : "   "}${i + 1}. `;
    const text = i === labels.length - 1 && reason ? `${label}: ${reason}` : label;
    const pad = " ".repeat(Array.from(head).length);
    chunk(text, w - pad.length).forEach((part, j) => lines.push(j === 0 ? head + part : pad + part));
  });
  lines.push("", ` ${permissionLegend}`);
  return lines.map((line) => (Array.from(line).length > w ? Array.from(line).slice(0, w).join("") : line));
}

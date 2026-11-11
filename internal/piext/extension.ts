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
import { pathToFileURL } from "node:url";
import {
  type BashOperations,
  createBashToolDefinition,
  createLocalBashOperations,
  type ExtensionAPI,
  type ExtensionContext,
} from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const bin = process.env.GATE_INBOX_BIN ?? "";
const statusFile = process.env.GATE_INBOX_STATUS_FILE ?? "";
// skipPermissions is a launch with its permission prompts off. Every gate in
// this extension stops asking under it; what still holds is what never asks:
// the sandbox around bash, and the OS confinement around the whole session.
const skipPermissions = process.env.GATE_INBOX_SKIP_PERMISSIONS === "1";

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
  sandboxedBash(pi);

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
}

// The sandboxed bash tool.
//
// pi's bash runs every command with pi's own permissions. This replaces it
// with the same tool whose commands run inside @anthropic-ai/sandbox-runtime
// (bubblewrap on Linux, sandbox-exec on macOS), the runtime Claude Code's own
// sandbox is built on. What a command may read, write and reach is the
// board's answer to `hook pi sandbox` (piext.Plan): the operator's Claude
// sandbox settings plus the board's key directory, and no tmux socket.
//
// It fails closed. When the board does not answer, the runtime will not
// load, or bubblewrap or socat is missing, every command asks first and
// says why. A command the model asks to run outside the sandbox
// (dangerouslyDisableSandbox) asks too. Only `[tools.pi] sandbox = "off"` in
// the board's config runs commands unsandboxed without asking, or a launch
// with permissions skipped, which still runs every other command sandboxed.

type SandboxManagerLike = {
  isSupportedPlatform(): boolean;
  checkDependencies(): { errors: string[]; warnings: string[] };
  initialize(config: unknown): Promise<void>;
  wrapWithSandbox(command: string, binShell?: string, customConfig?: unknown, abortSignal?: AbortSignal): Promise<string>;
  cleanupAfterCommand(): void;
  reset(): Promise<void>;
};

type SandboxState =
  | { mode: "on"; manager: SandboxManagerLike; tmpDir: string; allowUnsandboxed: boolean }
  | { mode: "off" }
  | { mode: "unavailable"; reason: string };

// sandboxStartMs bounds the board's answer, and separately the runtime's
// start (its network proxies); past either the sandbox is unavailable.
const sandboxStartMs = 20000;

let sandbox: Promise<SandboxState> | undefined;

async function startSandbox(cwd: string): Promise<SandboxState> {
  const plan = (await runHook("sandbox", { cwd }, sandboxStartMs)) as
    | { mode?: string; reason?: string; runtime?: string; tmpDir?: string; allowUnsandboxed?: boolean; config?: unknown }
    | undefined;
  if (!plan || typeof plan !== "object") return { mode: "unavailable", reason: "the board did not answer" };
  if (plan.mode === "off") return { mode: "off" };
  if (plan.mode !== "on" || !plan.runtime || !plan.tmpDir) {
    return { mode: "unavailable", reason: plan.reason || "the board gave no sandbox" };
  }
  try {
    const runtime = (await import(pathToFileURL(plan.runtime).href)) as { SandboxManager: SandboxManagerLike };
    const manager = runtime.SandboxManager;
    if (!manager.isSupportedPlatform()) return { mode: "unavailable", reason: `${process.platform} is not supported` };
    const deps = manager.checkDependencies();
    if (deps.errors.length > 0) return { mode: "unavailable", reason: deps.errors.join("; ") };
    let timer: NodeJS.Timeout | undefined;
    const timedOut = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("the sandbox did not start in time")), sandboxStartMs);
    });
    try {
      await Promise.race([manager.initialize(plan.config), timedOut]);
    } finally {
      clearTimeout(timer);
    }
    return { mode: "on", manager, tmpDir: plan.tmpDir, allowUnsandboxed: plan.allowUnsandboxed === true };
  } catch (err) {
    return { mode: "unavailable", reason: err instanceof Error ? err.message : String(err) };
  }
}

// sandboxedOperations runs each command through the runtime's wrapper, with
// the session's private TMPDIR.
function sandboxedOperations(state: Extract<SandboxState, { mode: "on" }>, local: BashOperations): BashOperations {
  return {
    exec: async (command, cwd, options) => {
      const wrapped = await state.manager.wrapWithSandbox(command, undefined, undefined, options.signal);
      try {
        return await local.exec(wrapped, cwd, { ...options, env: { ...(options.env ?? process.env), TMPDIR: state.tmpDir } });
      } finally {
        state.manager.cleanupAfterCommand();
      }
    },
  };
}

// askToRunUnsandboxed asks the person at the pane whether one command may run
// outside the sandbox. With no one to ask, the answer is no.
async function askToRunUnsandboxed(ctx: ExtensionContext, why: string, command: string, signal?: AbortSignal): Promise<boolean> {
  if (!ctx.hasUI) return false;
  const choice = await ctx.ui.select(`Bash command, outside the sandbox (${why})\n  ${command}\nDo you want to proceed?`, ["Yes", "No"], { signal });
  return choice === "Yes";
}

function sandboxedBash(pi: ExtensionAPI) {
  const local = createLocalBashOperations();
  const ready = (cwd: string) => (sandbox ??= startSandbox(cwd));
  const base = createBashToolDefinition(process.cwd());
  pi.on("session_start", async (_event, ctx) => {
    void ready(ctx.cwd);
  });
  // reset stops the runtime's proxies, so the next session pi starts in this
  // process builds its sandbox afresh rather than wrapping commands for
  // proxies that are gone.
  pi.on("session_shutdown", async () => {
    const state = await sandbox;
    sandbox = undefined;
    if (state?.mode === "on") await state.manager.reset().catch(() => undefined);
  });
  pi.registerTool({
    ...base,
    description:
      `${base.description} Commands run in a sandbox: writes are limited to the working directory and $TMPDIR, ` +
      "network to allowed hosts, and some paths cannot be read. If a command fails because of the sandbox, " +
      "retry it with dangerouslyDisableSandbox: true, which asks the user first.",
    parameters: Type.Object({
      ...base.parameters.properties,
      dangerouslyDisableSandbox: Type.Optional(
        Type.Boolean({ description: "Run this command outside the sandbox. The user is asked first." }),
      ),
    }),
    async execute(id, params, signal, onUpdate, ctx) {
      const { dangerouslyDisableSandbox, ...input } = params as typeof params & { dangerouslyDisableSandbox?: boolean };
      const state = await ready(ctx.cwd);
      let operations = local;
      if (state.mode === "on" && !(dangerouslyDisableSandbox === true && state.allowUnsandboxed)) {
        operations = sandboxedOperations(state, local);
      } else if (state.mode !== "off") {
        const why = state.mode === "unavailable" ? `sandbox unavailable: ${state.reason}` : "requested by the model";
        if (!skipPermissions && !(await askToRunUnsandboxed(ctx, why, input.command, signal))) {
          throw new Error(`Not run: the user did not allow it outside the sandbox (${why}).`);
        }
      }
      return createBashToolDefinition(ctx.cwd, { operations }).execute(id, input, signal, onUpdate, ctx);
    },
  });
}

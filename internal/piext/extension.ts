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
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
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

// The board's MCP server, under the name Claude Code knows it by. Pi turns
// every character of a tool name other than letters, digits and _ into _,
// so its tools reach the model as mcp__gate_inbox__*; the steering names
// them by their bare names (report_status, rename), which read the same in
// both.
const mcpServer = "gate-inbox";
const renameTool = `mcp__${mcpServer.replace(/[^A-Za-z0-9_]/g, "_")}__rename`;

type McpServerConfig = Parameters<ExtensionAPI["registerMcpServer"]>[1];

// mcpConfigFile is the launch's MCP config for this session (Go:
// piext.MCPConfigEnv), in the mcpServers shape Claude Code reads: the
// shared server with this session's caller token, or a server of its own.
const mcpConfigFile = process.env.GATE_INBOX_MCP_CONFIG ?? "";

// boardServerConfig turns the launch's mcpServers entry into what
// pi.registerMcpServer takes. The shapes match but for the timeout, which
// Claude Code reads in milliseconds and pi in seconds. Every tool is
// declared to the model directly, as Claude Code declares them.
function boardServerConfig(): McpServerConfig {
  const parsed = JSON.parse(readFileSync(mcpConfigFile, "utf8")) as {
    mcpServers?: Record<string, Record<string, unknown>>;
  };
  const entry = parsed.mcpServers?.[mcpServer];
  if (!entry) throw new Error(`${mcpConfigFile} has no ${mcpServer} server`);
  const config: Record<string, unknown> = {
    exposure: "direct",
    description: "Gate Inbox board: sessions, status, permissions",
  };
  if (typeof entry.url === "string") {
    config.url = entry.url;
    config.headers = entry.headers;
    if (typeof entry.timeout === "number") config.timeout = Math.ceil(entry.timeout / 1000);
  } else {
    config.command = entry.command;
    config.args = entry.args;
    config.env = entry.env;
  }
  return config as McpServerConfig;
}

// overridingConfigs names the pi config files that define a server under
// the board's name. Pi lets a file-configured server win over one an
// extension registers, and counts - and _ as the same character, so such an
// entry would silently take the board's tools over.
function overridingConfigs(cwd: string): string[] {
  const agentDir = process.env.PI_CODING_AGENT_DIR || join(homedir(), ".pi", "agent");
  const same = (name: string) => name.replace(/-/g, "_") === mcpServer.replace(/-/g, "_");
  const found: string[] = [];
  for (const file of [join(agentDir, "mcp.json"), join(cwd, ".pi", "mcp.json")]) {
    try {
      const parsed = JSON.parse(readFileSync(file, "utf8")) as { mcpServers?: Record<string, unknown> };
      if (Object.keys(parsed.mcpServers ?? {}).some(same)) found.push(file);
    } catch {
      // No file, or one pi will report itself.
    }
  }
  return found;
}

// boardTools gives the session the board's MCP tools and standing
// instructions, and keeps the session's name and the board row's in step.
// Its registration is for this run only; nothing is written to ~/.pi.
function boardTools(pi: ExtensionAPI) {
  let registerError = "";
  if (mcpConfigFile) {
    try {
      pi.registerMcpServer(mcpServer, boardServerConfig());
    } catch (err) {
      registerError = err instanceof Error ? err.message : String(err);
    }
  }

  // Fetched once a session: the text is the board's, fixed for the build.
  let steering = "";
  // The name last mirrored either way, so the change pi raises for a name
  // the extension set is not sent back as a rename of its own.
  let mirrored: string | undefined;

  pi.on("session_start", async (_event, ctx) => {
    if (registerError) {
      ctx.ui.notify(`Gate Inbox: the board's MCP tools are not registered: ${registerError}`, "error");
    }
    const overrides = overridingConfigs(ctx.cwd);
    if (overrides.length > 0) {
      ctx.ui.notify(
        `Gate Inbox: ${overrides.join(" and ")} defines a "${mcpServer}" MCP server, which overrides the board's; this session's board tools come from that entry.`,
        "warning",
      );
      await runHook("mcp_override", { sources: overrides });
    }
    mirrored = pi.getSessionName();
    if (!steering) {
      const answer = (await runHook("steering", {})) as { steering?: unknown } | undefined;
      if (typeof answer?.steering === "string") steering = answer.steering;
    }
  });

  // A section rather than a rewritten prompt: pi patches only a section
  // that changed, so the model's cached prefix survives every turn after
  // the first.
  pi.on("before_agent_start", async (event) => {
    if (steering) event.systemPromptOptions.sections[mcpServer] = steering;
  });

  // The board's rename names the row; the session's own name follows it,
  // so pi's session list and the board agree.
  pi.on("tool_result", async (event) => {
    if (event.toolName !== renameTool || event.isError) return;
    const name = typeof event.input?.name === "string" ? event.input.name.trim() : "";
    if (!name || name === mirrored) return;
    mirrored = name;
    pi.setSessionName(name);
  });

  // Pi's own /name renames the board row too.
  pi.on("session_info_changed", async (event) => {
    if (!event.name || event.name === mirrored) return;
    mirrored = event.name;
    await runHook("session_info_changed", { name: event.name });
  });
}

export default function gateInbox(pi: ExtensionAPI) {
  if (!bin || !statusFile) return;
  boardTools(pi);

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

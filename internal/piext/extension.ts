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
import { readdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";
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
  return runBin(["hook", "pi", verb], payload, timeoutMs);
}

// runClaudeHook runs one of the `$GATE_INBOX_BIN hook <verb>` commands the
// board's Claude Code hooks run, on the same payload Claude Code sends, so a
// pi session gets the very note a claude one does.
function runClaudeHook(verb: string, payload: unknown): Promise<unknown> {
  return runBin(["hook", verb], payload, hookTimeoutMs);
}

function runBin(args: string[], payload: unknown, timeoutMs: number): Promise<unknown> {
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
      child = spawn(bin, args, { stdio: ["pipe", "pipe", "ignore"] });
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
    mcpServers?: Record<string, ClaudeServerEntry>;
  };
  const entry = parsed.mcpServers?.[mcpServer];
  if (!entry) throw new Error(`${mcpConfigFile} has no ${mcpServer} server`);
  const shared = { exposure: "direct", description: "Gate Inbox board: sessions, status, permissions" } as const;
  if (entry.url) {
    const timeout = entry.timeout ? Math.ceil(entry.timeout / 1000) : undefined;
    return { ...shared, url: entry.url, headers: entry.headers, timeout };
  }
  if (!entry.command) throw new Error(`${mcpConfigFile}: the ${mcpServer} server has neither url nor command`);
  return { ...shared, command: entry.command, args: entry.args, env: entry.env };
}

// ClaudeServerEntry is the part of a Claude Code mcpServers entry the
// launch writes; timeout is in milliseconds.
type ClaudeServerEntry = {
  url?: string;
  headers?: Record<string, string>;
  timeout?: number;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
};

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

// hookNote reads the model-facing text out of a Claude Code hook answer:
// the context a UserPromptSubmit hook adds, or the reason a Stop hook blocks
// with.
function hookNote(answer: unknown): string {
  const a = answer as { hookSpecificOutput?: { additionalContext?: unknown }; reason?: unknown } | undefined;
  const note = a?.hookSpecificOutput?.additionalContext ?? a?.reason;
  return typeof note === "string" ? note : "";
}

const noteType = "gate-inbox-note";

// sessionNotes gives a pi session the notes Claude Code's prompt and stop
// hooks give a claude one: who a message typed into the session is from,
// checked against its Gate Inbox seal (`hook prompt-submit`), and which of
// the session's children wait on a dialog (`hook child-dialogs`). Each note
// is the Go side's, word for word.
function sessionNotes(pi: ExtensionAPI) {
  // Notes for the run the submitted prompt is about to start.
  let pending: string[] = [];
  let piSession = "";

  pi.on("session_start", async (_event, ctx) => {
    piSession = ctx.sessionManager.getSessionId();
  });

  // The prompt as typed, before pi expands it: the seal is in the text the
  // board pasted, and an expansion could move or drop it.
  pi.on("input", async (event) => {
    if (event.source === "extension") return { action: "continue" };
    const [seal, children] = await Promise.all([
      runClaudeHook("prompt-submit", { hook_event_name: "UserPromptSubmit", session_id: piSession, prompt: event.text }),
      runClaudeHook("child-dialogs", { hook_event_name: "UserPromptSubmit", session_id: piSession }),
    ]);
    const notes = [hookNote(seal), hookNote(children)].filter(Boolean);
    if (notes.length === 0) return { action: "continue" };
    if (event.streamingBehavior) {
      // Typed while a run goes on: the note follows the message into it.
      pi.sendMessage({ customType: noteType, content: notes.join("\n\n"), display: true }, { deliverAs: event.streamingBehavior });
    } else {
      pending = notes;
    }
    return { action: "continue" };
  });

  pi.on("before_agent_start", async (event) => {
    if (event.messageType !== "user" || pending.length === 0) return;
    const content = pending.join("\n\n");
    pending = [];
    return { message: { customType: noteType, content, display: true } };
  });

  // Claude Code's Stop hook can keep a turn going with a reason; pi's last
  // chance to do the same is agent_before_settle, which may append a message
  // and ask for one more provider request. turn_end would fire after every
  // tool round, not once the run is over.
  pi.on("agent_before_settle", async (event) => {
    const note = hookNote(await runClaudeHook("child-dialogs", { hook_event_name: "Stop", session_id: piSession }));
    if (!note) return;
    return {
      entries: [...event.entries, { type: "custom_message", customType: noteType, content: note, display: true }],
      continue: true,
    };
  });
}

// pluginDir is the board's Claude Code plugin (Go: piext.PluginDirEnv).
const pluginDir = process.env.GATE_INBOX_PLUGIN_DIR ?? "";

// commandTimeoutMs bounds one shell line a command template runs.
const commandTimeoutMs = 30000;

// runShell runs one line of a command template with sh and answers what it
// printed, its errors included, as Claude Code puts a !`command` line's
// output into the prompt.
function runShell(line: string, cwd: string): Promise<string> {
  return new Promise((resolve) => {
    let out = "";
    let child;
    try {
      child = spawn("sh", ["-c", line], { cwd, stdio: ["ignore", "pipe", "pipe"] });
    } catch (err) {
      resolve(`(could not run: ${err instanceof Error ? err.message : String(err)})`);
      return;
    }
    const timer = setTimeout(() => child.kill("SIGKILL"), commandTimeoutMs);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => (out += chunk));
    child.stderr.on("data", (chunk: string) => (out += chunk));
    child.on("error", (err: Error) => {
      clearTimeout(timer);
      resolve(`(could not run: ${err.message})`);
    });
    child.on("close", () => {
      clearTimeout(timer);
      resolve(out.trimEnd());
    });
  });
}

// commandTemplate splits a Claude Code command file into its frontmatter
// description and its body.
function commandTemplate(file: string): { description: string; body: string } {
  const text = readFileSync(file, "utf8");
  const match = /^---\n([\s\S]*?)\n---\n?/.exec(text);
  const front = match?.[1] ?? "";
  const description = /^description:\s*(.*)$/m.exec(front)?.[1]?.trim() ?? "";
  return { description, body: match ? text.slice(match[0].length) : text };
}

// expandTemplate does what Claude Code does to a command body: $ARGUMENTS
// becomes the arguments, and each !`command` line becomes its output.
async function expandTemplate(body: string, args: string, cwd: string): Promise<string> {
  const lines = body.replaceAll("$ARGUMENTS", args).split("\n");
  const expanded = await Promise.all(
    lines.map((line) => {
      const shell = /^!`(.+)`\s*$/.exec(line);
      return shell ? runShell(shell[1], cwd) : Promise.resolve(line);
    }),
  );
  return expanded.join("\n").trim();
}

// boardSkills offers a pi session the board's skills and slash commands from
// the plugin a claude launch loads, so the two CLIs read one copy: skills/
// through resources_discover (as /skill:<name>), and each commands/<name>.md
// as /<name>, expanded the way Claude Code expands it and sent as the prompt.
function boardSkills(pi: ExtensionAPI) {
  if (!pluginDir) return;
  pi.on("resources_discover", async () => ({ skillPaths: [join(pluginDir, "skills")] }));

  let files: string[] = [];
  try {
    files = readdirSync(join(pluginDir, "commands")).filter((f) => f.endsWith(".md"));
  } catch {
    return;
  }
  for (const file of files) {
    const path = join(pluginDir, "commands", file);
    let template;
    try {
      template = commandTemplate(path);
    } catch {
      continue;
    }
    const { description, body } = template;
    pi.registerCommand(basename(file, ".md"), {
      description,
      handler: async (args, ctx) => {
        const prompt = await expandTemplate(body, args.trim(), ctx.cwd);
        if (ctx.isIdle()) pi.sendUserMessage(prompt);
        else pi.sendUserMessage(prompt, { deliverAs: "followUp" });
      },
    });
  }
}

export default function gateInbox(pi: ExtensionAPI) {
  if (!bin || !statusFile) return;
  boardTools(pi);
  sessionNotes(pi);
  boardSkills(pi);

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

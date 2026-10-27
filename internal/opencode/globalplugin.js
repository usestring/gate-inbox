// Gate Inbox writes, rewrites and removes this file; edits to it do not last.
// It gives an opencode session the board adopted what a launched one has, and
// does nothing for any other session: see internal/opencode/globalplugin.go.
import { accessSync, constants, readFileSync, readdirSync, renameSync, writeFileSync } from "fs";

const home = __HOME__;
const bin = __BIN__;
const tools = __TOOLS__;
const steering = __STEERING__;

const hooksDir = home + "/hooks";
const adoptedDir = hooksDir + "/adopted";
const rowPattern = /^[A-Za-z0-9_-]+$/;
const panePattern = /^%[0-9]+$/;
const markerName = /^([0-9]+)(%[0-9]+)$/;

const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return err?.code === "EPERM";
  }
};

// The board is running and its binary is still installed. Anything else,
// including Gate Inbox deleted without unregistering, leaves every hook inert.
const boardUp = () => {
  try {
    const pid = parseInt(readFileSync(home + "/manager.lock", "utf8"), 10);
    if (!(pid > 0) || !alive(pid)) return false;
    accessSync(bin, constants.X_OK);
    return true;
  } catch {
    return false;
  }
};

// readMarker is the adoption marker for one pane, when it names a live
// opencode-styled agent: "<row> <pid> <tool> [<conversation>]". A claude's
// two-field marker, or another CLI's, names a row this session is not.
const readMarker = (server, pane) => {
  try {
    const fields = readFileSync(adoptedDir + "/" + server + pane, "utf8").trim().split(/\s+/);
    if (fields.length < 3 || fields.length > 4 || !rowPattern.test(fields[0])) return undefined;
    if (!tools.includes(fields[2])) return undefined;
    const pid = parseInt(fields[1], 10);
    if (!(pid > 0) || !alive(pid)) return undefined;
    return { row: fields[0], pid, server, pane, conversation: fields[3] ?? "" };
  } catch {
    return undefined;
  }
};

// paneMarker reads the marker for the pane a shell's $TMUX and $TMUX_PANE
// name. Those are the opencode client's own, which it hands the service for
// every session it shows.
const paneMarker = (env) => {
  const pane = env?.TMUX_PANE;
  const server = String(env?.TMUX ?? "").split(",")[1] ?? "";
  if (!panePattern.test(pane ?? "") || !/^[0-9]+$/.test(server)) return undefined;
  return readMarker(server, pane);
};

// markedSession finds the marker the board wrote for a row already bound to
// this conversation, so a session is recognised again after the plugin
// reloads or the service restarts.
const markedSession = (sessionID) => {
  let names;
  try {
    names = readdirSync(adoptedDir);
  } catch {
    return undefined;
  }
  for (const name of names) {
    const parts = markerName.exec(name);
    if (!parts) continue;
    const marker = readMarker(parts[1], parts[2]);
    if (marker && marker.conversation === sessionID) return marker;
  }
  return undefined;
};

const writeWhole = (path, content) => {
  const staging = path + "." + process.pid + ".part";
  writeFileSync(staging, content, { mode: 0o644 });
  renameSync(staging, path);
};

export default {
  id: "gate-inbox-" + __KEY__,
  setup: async (ctx) => {
    // sessionID -> marker of the row that session is, learned from its shell.
    const bound = new Map();
    // command -> sessionID of the shell tool call about to run it.
    const pending = new Map();
    const reported = new Map();

    const bind = (sessionID, marker) => {
      bound.set(sessionID, marker);
      if (marker.conversation === sessionID || reported.get(marker.row) === sessionID) return;
      // The poller binds the row to this conversation and removes the file.
      writeWhole(hooksDir + "/" + marker.row + ".conversation", sessionID + "\n");
      reported.set(marker.row, sessionID);
    };

    await ctx.tool.hook("execute.before", (call) => {
      try {
        const command = call?.input?.command;
        if ((call?.tool === "shell" || call?.tool === "bash") && call.sessionID && typeof command === "string") {
          pending.set(command, call.sessionID);
          if (pending.size > 64) pending.delete(pending.keys().next().value);
        }
      } catch {}
    });

    await ctx.shell.hook("create.before", (shell) => {
      try {
        const env = shell?.env;
        const sessionID = pending.get(shell?.command);
        pending.delete(shell?.command);
        // A session the board launched has its row in its environment
        // already, and its own server and config.
        if (!env || env.GATE_INBOX_SESSION_ID) return;
        const marker = paneMarker(env);
        if (!marker || !boardUp()) return;
        env.GATE_INBOX_SESSION_ID = marker.row;
        env.GATE_INBOX_BIN = bin;
        env.GATE_INBOX_HOME = home;
        if (sessionID) bind(sessionID, marker);
      } catch {}
    });

    await ctx.session.hook("context", (request) => {
      try {
        const sessionID = request?.sessionID;
        if (!sessionID || !Array.isArray(request.system)) return;
        if (!boardUp()) return;
        let marker = bound.get(sessionID);
        if (marker) {
          const current = readMarker(marker.server, marker.pane);
          if (!current || current.row !== marker.row) {
            bound.delete(sessionID);
            marker = undefined;
          }
        }
        if (!marker) {
          marker = markedSession(sessionID);
          if (!marker) return;
          bound.set(sessionID, marker);
        }
        request.system.push({ type: "text", text: steering });
      } catch {}
    });
  },
};

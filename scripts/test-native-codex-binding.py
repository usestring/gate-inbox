#!/usr/bin/env python3
"""Never connects to live state: all homes, providers, processes and sockets are fixtures."""

import argparse
import json
import os
import pathlib
import shlex
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser(
    description="Offline, private native Codex caller-binding regression"
)
parser.add_argument("--gate-inbox-binary", required=True)
parser.add_argument("--codex-binary", required=True)
parser.add_argument(
    "--managed-daemon",
    action="store_true",
    help="test the default daemon control socket",
)
parser.add_argument(
    "--stop-script", help="optional company nuke script to exercise instead of stop"
)
parser.add_argument(
    "--default-launch",
    choices=["managed", "adopted"],
    help="exercise ordinary bound-daemon managed launch or an adopted client without managed env",
)
parser.add_argument(
    "--cold-daemon",
    action="store_true",
    help="prove automatic native daemon start in a disposable home",
)
parser.add_argument("--flow", choices=["start", "resume", "fork"], default="start")
parser.add_argument(
    "--client-argv-json",
    help="private adopted fixture command emitted by a launcher; executable must match --codex-binary",
)
options = parser.parse_args()
if not __debug__:
    raise RuntimeError("native fixture verification requires Python assertions")
gate = str(pathlib.Path(options.gate_inbox_binary).resolve(strict=True))
codex = str(pathlib.Path(options.codex_binary).resolve(strict=True))
root = pathlib.Path(tempfile.mkdtemp(prefix="gi-native-", dir="/tmp"))
home = root / "codex"
state = root / "gate"
home.mkdir()
state.mkdir()
(root / "bin").mkdir()
(root / "bin" / "gate-inbox").symlink_to(gate)
(root / "bin" / "codex").symlink_to(codex)
env = {
    "PATH": str(root / "bin") + os.pathsep + os.environ["PATH"],
    "HOME": str(root),
    "CODEX_HOME": str(home),
    "GATE_INBOX_HOME": str(state),
    "LANG": "C.UTF-8",
    "GATE_INBOX_TRACES": "off",
    "TMUX_TMPDIR": str(root),
    "HTTP_PROXY": "http://127.0.0.1:1",
    "HTTPS_PROXY": "http://127.0.0.1:1",
    "ALL_PROXY": "http://127.0.0.1:1",
    "NO_PROXY": "127.0.0.1,localhost",
}
socket = "native-binding"
server = str(root / "daemon.sock")
server_args = ["app-server", "--listen", "unix://" + server]
if options.managed_daemon or options.default_launch or options.cold_daemon:
    server = str(home / "app-server-control" / "app-server-control.sock")
    server_args = ["app-server", "--listen", "unix://", "--managed-daemon"]

stop = shlex.quote(gate) + " stop --if-managed --json"
if options.stop_script:
    stop = "bash " + shlex.quote(
        str(pathlib.Path(options.stop_script).resolve(strict=True))
    )
command = (
    "python3 -c "
    + shlex.quote(
        "import os,json;json.dump({k:os.environ.get(k) for k in ['CODEX_THREAD_ID','CODEX_TOOL_CALL_ID','GATE_INBOX_CALLER_BINDING','GATE_INBOX_HOME']},open("
        + repr(str(root / "runtime.json"))
        + ",'w'))"
    )
    + ("; env -u GATE_INBOX_SESSION_ID -u GATE_INBOX_BIN " + "TMUX_PANE=%999999 ")
    + stop
    + " > "
    + shlex.quote(str(root / "stop.json"))
    + " 2> "
    + shlex.quote(str(root / "stop.err"))
    + '; result=$?; exit "$result"'
)


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        tools = payload.get("tools", [])
        tool = next((t for t in tools if t.get("name") == "exec_command"), None)
        is_target = "native-binding-fixture" in json.dumps(payload)
        if tool and is_target and not getattr(self.server, "called", False):
            self.server.called = True
            item = {
                "type": "function_call",
                "id": "fc_fixture",
                "call_id": "call_fixture",
                "name": "exec_command",
                "arguments": json.dumps({"cmd": command, "yield_time_ms": 1000}),
            }
        else:
            item = {
                "type": "message",
                "id": "msg_fixture",
                "role": "assistant",
                "content": [{"type": "output_text", "text": "Fixture complete."}],
            }
        events = [
            (
                "response.created",
                {
                    "type": "response.created",
                    "response": {"id": "fixture", "status": "in_progress"},
                },
            ),
            (
                "response.output_item.added",
                {"type": "response.output_item.added", "output_index": 0, "item": item},
            ),
            (
                "response.output_item.done",
                {"type": "response.output_item.done", "output_index": 0, "item": item},
            ),
            (
                "response.completed",
                {
                    "type": "response.completed",
                    "response": {
                        "id": "fixture",
                        "status": "completed",
                        "output": [item],
                        "usage": {"input_tokens": 1, "output_tokens": 1},
                    },
                },
            ),
        ]
        for kind, body in events:
            try:
                self.wfile.write(
                    ("event: " + kind + "\ndata: " + json.dumps(body) + "\n\n").encode()
                )
                self.wfile.flush()
            except BrokenPipeError:
                break

    def log_message(self, *args):
        pass


http = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
worker = threading.Thread(target=http.serve_forever, daemon=True)
worker.start()
(home / "config.toml").write_text(f"""model_provider = "fixture"
model = "gpt-5.2"
approval_policy = "never"
sandbox_mode = "danger-full-access"
[features]
plugins = false
apps = false
code_mode_host = false
[projects."{root}"]
trust_level = "trusted"
[model_providers.fixture]
name = "offline fixture"
base_url = "http://127.0.0.1:{http.server_port}"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
""")
(state / "config.toml").write_text('tmux_socket = "' + socket + '"\n')
log = (root / "daemon.log").open("w")
proc = None
native_managed = bool(
    options.default_launch or options.cold_daemon or options.managed_daemon
)
if native_managed:
    package = home / "packages" / "app-server-daemon"
    (package / "fixture" / "bin").mkdir(parents=True)
    shutil.copy2(codex, package / "fixture" / "bin" / "codex")
    (package / "current").symlink_to("fixture")
    (home / "app-server-daemon").mkdir()
    (home / "app-server-daemon" / "settings.json").write_text(
        json.dumps({"updater": {"autoUpdateEnabled": False}})
    )
    if not options.cold_daemon:
        subprocess.run(
            [codex, "app-server", "daemon", "start"],
            cwd=root,
            env=env,
            stdout=log,
            stderr=log,
            check=True,
            timeout=30,
        )
else:
    proc = subprocess.Popen(
        [codex, *server_args],
        cwd=root,
        env=env,
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=log,
    )


def tmux(*args):
    return subprocess.check_output(
        ["tmux", "-L", socket, *args], env=env, text=True
    ).strip()


try:
    deadline = time.monotonic() + 10
    while (
        not options.cold_daemon
        and not pathlib.Path(server).exists()
        and time.monotonic() < deadline
    ):
        time.sleep(0.05)
    assert proc is None or proc.poll() is None, (root / "daemon.log").read_text()
    subprocess.run(
        [gate, "sessions", "--json"],
        cwd=root,
        env=env,
        capture_output=True,
        text=True,
        timeout=10,
    )
    tmux("new-session", "-d", "-s", "sentinel", "sleep 120")
    parent_pane = tmux("display-message", "-p", "-t", "sentinel", "#{pane_id}")
    parent_db = sqlite3.connect(state / "state.db")
    parent_db.execute(
        "INSERT INTO sessions (id,name,tool,cwd,group_name,status,created_at,last_status_at,tmux_socket,tmux_pane_id) VALUES (?,?,?,?,?,?,?,?,?,?)",
        (
            "cafe0001",
            "fixture-parent",
            "sh",
            str(root),
            "",
            "working",
            int(time.time()),
            int(time.time()),
            socket,
            parent_pane,
        ),
    )
    parent_db.commit()
    parent_db.close()
    launch = (
        "env GATE_INBOX_HOME="
        + shlex.quote(str(state))
        + " CODEX_HOME="
        + shlex.quote(str(home))
        + " "
        + shlex.quote(gate)
        + " codex-client"
        + ("" if native_managed else " --server " + shlex.quote(server))
        + " -- "
        + shlex.quote(codex)
        + " --no-alt-screen "
        + shlex.quote("native-binding-fixture")
    )
    seed_thread = None
    if options.flow != "start":
        assert (
            options.default_launch == "adopted"
        ), "resume/fork use the ordinary interactive launcher fixture"
        seed_launch = (
            "env CODEX_HOME="
            + shlex.quote(str(home))
            + " HOME="
            + shlex.quote(str(root))
            + " GATE_INBOX_HOME="
            + shlex.quote(str(state))
            + " "
            + shlex.join(
                [
                    gate,
                    "codex-client",
                    "--",
                    codex,
                    "--no-alt-screen",
                    "seed-binding-fixture",
                ]
            )
        )
        tmux("new-session", "-d", "-c", str(root), "-s", "seed", seed_launch)
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            rolls = list((home / "sessions").rglob("*.jsonl"))
            if rolls:
                records = [
                    json.loads(line) for line in rolls[0].read_text().splitlines()
                ]
                meta = next(
                    (r for r in records if r.get("type") == "session_meta"), None
                )
                if meta and "Fixture complete" in tmux(
                    "capture-pane", "-p", "-t", "seed"
                ):
                    seed_thread = meta["payload"]["id"]
                    break
            time.sleep(0.1)
        assert seed_thread, "disposable seed thread did not settle"
        time.sleep(0.5)
        tmux("kill-session", "-t", "seed")
        time.sleep(0.5)
    native_sentinel = None
    if not options.cold_daemon:
        sentinel_launch = (
            "env CODEX_HOME="
            + shlex.quote(str(home))
            + " HOME="
            + shlex.quote(str(root))
            + " GATE_INBOX_HOME="
            + shlex.quote(str(state))
            + " "
            + shlex.join(
                [gate, "codex-client"]
                + ([] if native_managed else ["--server", server])
                + ["--", codex, "--no-alt-screen", "sentinel-binding-fixture"]
            )
        )
        tmux(
            "new-session",
            "-d",
            "-c",
            str(root),
            "-s",
            "native-sentinel",
            sentinel_launch,
        )
        deadline = time.monotonic() + 20
        while (
            "Fixture complete"
            not in tmux("capture-pane", "-p", "-t", "native-sentinel")
            and time.monotonic() < deadline
        ):
            time.sleep(0.1)
        assert "Fixture complete" in tmux(
            "capture-pane", "-p", "-t", "native-sentinel"
        ), "native sentinel did not settle"
        native_sentinel = tmux(
            "display-message", "-p", "-t", "native-sentinel", "#{pane_id} #{pane_pid}"
        )
        processes = subprocess.check_output(
            ["ps", "-ax", "-o", "pid=,ppid=,rss=,command="], text=True
        )
        entries = [line.strip().split(None, 3) for line in processes.splitlines()]
        bridge_pid = native_sentinel.split()[1]
        samples = [e for e in entries if e[0] == bridge_pid or e[1] == bridge_pid]
        print(
            "Native bridge/client RSS samples (KiB): "
            + json.dumps(
                [
                    {
                        "pid": e[0],
                        "rss": e[2],
                        "role": "bridge" if "codex-client" in e[3] else "client",
                    }
                    for e in samples
                ]
            ),
            flush=True,
        )
    target_id = "native01"
    target_session = "gi_native01"
    if options.default_launch == "managed":
        spawned = subprocess.run(
            [
                gate,
                "spawn",
                "--tool",
                "codex",
                "--name",
                "native-default",
                "--prompt",
                "native-binding-fixture",
                "--directory",
                str(root),
                "--json",
            ],
            cwd=root,
            env=dict(env, GATE_INBOX_SESSION_ID="cafe0001"),
            capture_output=True,
            text=True,
            timeout=10,
        )
        assert spawned.returncode == 0, spawned.stderr
        target_id = json.loads(spawned.stdout)["id"]
        target_session = "gi_" + target_id
    else:
        if options.default_launch == "adopted":
            client_argv = [
                gate,
                "codex-client",
                "--",
                codex,
                "--no-alt-screen",
                "native-binding-fixture",
            ]
            if options.client_argv_json:
                client_argv = json.loads(
                    pathlib.Path(options.client_argv_json).read_text()
                )
                assert isinstance(client_argv, list) and all(
                    isinstance(arg, str) for arg in client_argv
                )
                assert (
                    str(
                        pathlib.Path(
                            shutil.which(client_argv[0], path=env["PATH"])
                            or client_argv[0]
                        ).resolve(strict=True)
                    )
                    == gate
                )
                assert client_argv[1:] == [
                    "codex-client",
                    "--",
                    "codex",
                    "--no-alt-screen",
                    "native-binding-fixture",
                ], "launcher fixture accepts only the bound offline new-client command"
            if seed_thread:
                client_argv = client_argv[:-1] + [
                    options.flow,
                    seed_thread,
                    "native-binding-fixture",
                ]
            launch = (
                "env -u GATE_INBOX_SESSION_ID -u GATE_INBOX_BIN GATE_INBOX_HOME="
                + shlex.quote(str(state))
                + " CODEX_HOME="
                + shlex.quote(str(home))
                + " HOME="
                + shlex.quote(str(root))
                + " "
                + shlex.join(client_argv)
            )
        tmux("new-session", "-d", "-c", str(root), "-s", target_session, launch)
    sentinel = tmux("display-message", "-p", "-t", "sentinel", "#{pane_id} #{pane_pid}")
    pane = tmux("display-message", "-p", "-t", target_session, "#{pane_id}")
    db = sqlite3.connect(state / "state.db")
    now = int(time.time())
    if options.default_launch != "managed":
        db.execute(
            "INSERT INTO sessions (id,name,tool,cwd,group_name,status,created_at,last_status_at,agent_launched_at,tmux_socket,tmux_pane_id,agent_session_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
            (
                target_id,
                "native-bound",
                "codex",
                str(root),
                "",
                "working",
                now,
                now,
                now,
                socket,
                pane,
                "deliberately-unreliable",
            ),
        )
    if options.default_launch == "managed":
        db.execute(
            "UPDATE sessions SET agent_session_id=? WHERE id=?",
            ("deliberately-unreliable", target_id),
        )
    db.commit()
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if (
            db.execute(
                "SELECT status FROM sessions WHERE id=?", (target_id,)
            ).fetchone()[0]
            == "dead"
        ):
            break
        time.sleep(0.1)
    row = db.execute(
        "SELECT status,archived,agent_session_id FROM sessions WHERE id=?",
        (target_id,),
    ).fetchone()
    assert row == ("dead", 0, "deliberately-unreliable"), (
        row,
        (
            (root / "stop.err").read_text()
            if (root / "stop.err").exists()
            else "no caller output: " + tmux("capture-pane", "-p", "-t", target_session)
        ),
    )
    assert db.execute(
        "SELECT reason FROM session_ends WHERE session_id=?", (target_id,)
    ).fetchone() == ("killed",)
    assert (
        tmux("display-message", "-p", "-t", "sentinel", "#{pane_id} #{pane_pid}")
        == sentinel
    )
    if native_sentinel:
        assert (
            tmux(
                "display-message",
                "-p",
                "-t",
                "native-sentinel",
                "#{pane_id} #{pane_pid}",
            )
            == native_sentinel
        )
        assert "Fixture complete" in tmux("capture-pane", "-p", "-t", "native-sentinel")
    assert pane not in tmux("list-panes", "-a", "-F", "#{pane_id}").splitlines()
    if native_managed:
        info = json.loads(
            subprocess.check_output(
                [codex, "app-server", "daemon", "version"], env=env, text=True
            )
        )
        assert info["status"] == "running", "shared daemon stopped"
    else:
        assert proc.poll() is None, "shared daemon stopped"
    assert list((home / "sessions").rglob("*.jsonl")), "history was deleted"
    runtime = json.loads((root / "runtime.json").read_text())
    assert runtime["CODEX_THREAD_ID"]
    assert runtime["GATE_INBOX_CALLER_BINDING"]
    if seed_thread:
        assert (runtime["CODEX_THREAD_ID"] == seed_thread) == (options.flow == "resume")
    print(
        "PASS: native "
        + (options.default_launch or "bridged")
        + " "
        + options.flow
        + " caller stopped its own pane; intentional end, history, daemon and sentinel verified.",
        flush=True,
    )
    print(
        subprocess.check_output([codex, "--version"], env=env, text=True).strip(),
        flush=True,
    )
    db.close()

finally:
    subprocess.run(["tmux", "-L", socket, "kill-server"], env=env, capture_output=True)
    if native_managed:
        subprocess.run(
            [codex, "app-server", "daemon", "stop"],
            cwd=root,
            env=env,
            stdout=log,
            stderr=log,
            timeout=15,
        )
    elif proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            if proc.poll() is None:
                proc.kill()
            proc.wait(timeout=5)
    http.shutdown()
    http.server_close()
    worker.join(5)
    log.close()
    shutil.rmtree(root)

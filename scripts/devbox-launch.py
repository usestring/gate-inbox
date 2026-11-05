#!/usr/bin/env python3
"""Launch a manual Gate Inbox request over SSH, then attach its remote pane."""
import argparse
import json
import os
import posixpath
from pathlib import Path
import re
import shlex
import subprocess
import sys

REMOTE = r'''
import json, os, subprocess, sys
request = json.load(sys.stdin)
directory = os.path.expanduser(request["directory"])
if not os.path.isdir(directory):
    sys.exit("The mapped devbox directory does not exist: " + directory)
threshold = request.get("memory_available_below", 0)
if threshold:
    try:
        with open("/proc/meminfo") as stream:
            memory = dict((line.split()[0].rstrip(":"), int(line.split()[1])) for line in stream)
        available = 100 * memory["MemAvailable"] / memory["MemTotal"]
    except (OSError, KeyError, ValueError, ZeroDivisionError):
        sys.exit("Cannot check devbox memory; no session launched")
    if available < threshold:
        sys.exit("Devbox has %.1f%% memory available, below %.1f%%; no session launched" % (available, threshold))
args = ["gate-inbox", "spawn", "--nest=false", "--json", "--directory", directory,
        "--tool", request["tool"], "--name", request["name"]]
for key in ("model", "prompt"):
    if request.get(key): args.extend(["--" + key, request[key]])
if request.get("skip_permissions"): args.append("--skip-permissions")
env = os.environ.copy()
env["PATH"] = env.get("PATH", "") + os.pathsep + os.path.expanduser("~/.local/bin")
for key in ("GATE_INBOX_SESSION_ID", "GATE_INBOX_STATUS_FILE", "CLAUDECODE"):
    env.pop(key, None)
result = subprocess.run(args, text=True, capture_output=True, env=env, cwd=directory)
if result.returncode:
    sys.exit(result.stderr or result.stdout or "Devbox launch failed")
created = json.loads(result.stdout)
print(json.dumps({"id": created["id"]}))
'''


def mapped_directory(directory, mappings):
    source = os.path.realpath(os.path.expanduser(directory))
    roots = []
    for mapping in mappings:
        local, separator, remote = mapping.partition('=')
        if not separator or not local or not remote:
            raise ValueError('Each --map must be LOCAL_ROOT=REMOTE_ROOT')
        roots.append((os.path.realpath(os.path.expanduser(local)), remote))
    for local, remote in sorted(roots, key=lambda item: len(item[0]), reverse=True):
        if source == local or source.startswith(local.rstrip(os.sep) + os.sep):
            relative = os.path.relpath(source, local)
            return remote if relative == '.' else posixpath.join(remote, relative.replace(os.sep, '/'))
    raise ValueError('No devbox directory mapping for ' + directory)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', required=True, help='SSH destination, preferably an SSH config alias')
    parser.add_argument('--identity', help='Existing SSH identity file')
    parser.add_argument('--map', action='append', default=[], help='LOCAL_ROOT=REMOTE_ROOT; longest match wins')
    parser.add_argument('--socket', help='Remote tmux socket path when the board uses a private server')
    parser.add_argument('request', type=Path)
    options = parser.parse_args()
    if options.host.startswith('-'):
        parser.error('--host must be an SSH destination')
    request = json.loads(options.request.read_text())
    if request.get('attachments') or request.get('children_skip_permissions') or any(
        value.lower() not in ('', 'false', 'off', '0') for value in request.get('form', {}).values()
    ):
        parser.error('This launcher cannot transfer attachments or extension/child-permission settings; your local draft is preserved')
    request['directory'] = mapped_directory(request['directory'], options.map)
    ssh = ['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10']
    if options.identity:
        ssh.extend(['-i', os.path.expanduser(options.identity)])
    spawn = 'python3 -c ' + shlex.quote(REMOTE)
    result = subprocess.run(ssh + [options.host, spawn], input=json.dumps(request), text=True, capture_output=True)
    if result.returncode:
        print(result.stderr or result.stdout or 'SSH launch failed', file=sys.stderr)
        return result.returncode
    session_id = json.loads(result.stdout)['id']
    if not re.fullmatch(r'[A-Za-z0-9_-]+', session_id):
        raise ValueError('Devbox returned an invalid session id')
    print('Devbox session ' + session_id + ' started. Detach from tmux to return to the local board.', flush=True)
    attach = ['tmux']
    if options.socket:
        attach.extend(['-S', options.socket])
    attach.extend(['attach-session', '-t', 'gi_' + session_id])
    # A connection failure after creation must not make a retry create a duplicate.
    attached = subprocess.run(ssh + ['-tt', options.host, shlex.join(attach)])
    if attached.returncode:
        print('Session ' + session_id + ' is still on the devbox board; reconnect there to continue.', file=sys.stderr)
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError) as error:
        sys.exit(str(error))

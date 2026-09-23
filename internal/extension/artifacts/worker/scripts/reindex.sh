#!/usr/bin/env bash
# Fill in index rows for artifacts the index never saw: anything published
# before the index shipped, or stored by a publish whose index write failed.
#
# Idempotent -- a second run indexes nothing -- so it is safe to rerun after a
# deploy, safe to run when nothing is missing, and safe to rerun from the
# start after a failure part way. The Worker covers one page of the bucket per
# request; this follows its cursor to the end and prints the totals. Any
# answer other than 2xx stops it with a non-zero exit.
#
# ARTIFACT_BASE_URL is the Worker's URL. The signing key is read from stdin,
# so it can come straight from your secret store:
#
#   your-secret-store read ARTIFACT_SIGNING_KEY | ARTIFACT_BASE_URL=https://artifacts.example.com ./scripts/reindex.sh
set -euo pipefail

BASE="${ARTIFACT_BASE_URL:?set ARTIFACT_BASE_URL to the Worker URL}"

BASE="$BASE" python3 -c '
import base64, hashlib, hmac, json, os, subprocess, sys, time

# The key is read from stdin and never written anywhere: it signs each page.
key = sys.stdin.read().strip().encode()
b64 = lambda raw: base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
base = os.environ["BASE"].rstrip("/")

def page(cursor):
    # The body carries the cursor, and the signature covers the body.
    body = b"" if cursor is None else json.dumps({"cursor": cursor}).encode()
    stamp = str(int(time.time()))
    canonical = "\n".join(["POST", "/reindex", stamp, hashlib.sha256(body).hexdigest()])
    signature = b64(hmac.new(key, canonical.encode(), hashlib.sha256).digest())
    # --data-binary @- keeps the body off argv; -w appends the status on its
    # own line so a 401 or a 500 is an error here, not a reply to print.
    result = subprocess.run(
        ["curl", "-sS", "-X", "POST", "--data-binary", "@-", "-w", "\n%{http_code}",
         "-H", f"X-Artifact-Auth: v1:{stamp}:{signature}", f"{base}/reindex"],
        input=body, capture_output=True, check=True)
    reply, _, status = result.stdout.decode().rpartition("\n")
    if not status.startswith("2"):
        sys.exit(f"reindex: HTTP {status}: {reply.strip()}")
    try:
        return json.loads(reply)
    except ValueError:
        sys.exit(f"reindex: HTTP {status} with a reply that is not JSON: {reply.strip()}")

totals = {"scanned": 0, "indexed": 0, "skipped": 0}
cursor = None
while True:
    reply = page(cursor)
    for field in totals:
        totals[field] += reply[field]
    cursor = reply.get("cursor")
    if not cursor:
        break
print(json.dumps(totals))
'

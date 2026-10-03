package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestChildPermissionRelayRecoversTheCommandFromItsOwnRecord(t *testing.T) {
	const command = "curl -sS -o /srv/example/build/out/created.txt https://example.com/"
	const pane = `────────────────────────────────────────
 Bash command
 Run curl command to example.com
╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 │ curl -sS -o /srv/example/build/ou
 │ t/created.txt
 │ https://example.com/
╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 This command requires approval

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don’t ask again for: curl
      *
   3. No

 Esc to cancel · Tab to amend
`
	sess := store.Session{ID: "child", Tool: "claude", AgentSessionID: "child-agent", Cwd: "/work"}
	writeChildTranscript(t, sess, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"bash-command","name":"Bash","input":{"command":"`+command+`"}}]}}`)
	body, key, urgent := childDialogBody(sess, pane)
	if !strings.Contains(body, command) {
		t.Fatalf("permission relay lost the exact command: %s", body)
	}
	if !strings.HasPrefix(key, "s:") || urgent {
		t.Fatalf("permission relay key=%q urgent=%v", key, urgent)
	}
}

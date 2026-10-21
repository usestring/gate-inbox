package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/relayask"
	"github.com/usestring/gate-inbox/internal/store"
)

const curlCommand = "curl -sS -o /srv/example/build/out/created.txt https://example.com/"

const curlPane = `────────────────────────────────────────
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

func TestChildPermissionRelayRecoversTheCommandFromItsOwnRecord(t *testing.T) {
	const command, pane = curlCommand, curlPane
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

// The relay's AskUserQuestion input puts the prompt as a decision: the
// child's name and its call's description in the question, the command only
// in the options' previews.
func TestChildPermissionRelayAsksADecisionWithTheCommandInThePreview(t *testing.T) {
	sess := store.Session{ID: "child", Name: "curl-probe", Tool: "claude", AgentSessionID: "child-agent", Cwd: "/work"}
	writeChildTranscript(t, sess, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"bash-command","name":"Bash","input":{"command":"`+curlCommand+`","description":"Fetch example.com into the build output"}}]}}`)
	body, _, _ := childDialogBody(sess, curlPane)
	at := strings.LastIndex(body, `{"questions":`)
	if at < 0 {
		t.Fatalf("no AskUserQuestion input:\n%s", body)
	}
	var in relayask.Input
	if err := json.Unmarshal([]byte(body[at:]), &in); err != nil || len(in.Questions) != 1 {
		t.Fatalf("input %s: %v", body[at:], err)
	}
	q := in.Questions[0]
	if want := `curl-probe wants to run a Bash command: "Fetch example.com into the build output". Seen in the command: writes /srv/example/build/out/created.txt; names nothing it deletes; network: example.com, curl;`; !strings.HasPrefix(q.Question, want) {
		t.Errorf("question %q, want it to start %q", q.Question, want)
	}
	if strings.Contains(q.Question, curlCommand) || strings.Contains(body, "question: \"") {
		t.Errorf("the command is put in the question:\n%s", body)
	}
	for _, option := range q.Options {
		if !strings.Contains(option.Preview, curlCommand) {
			t.Errorf("option %q does not preview the command: %q", option.Label, option.Preview)
		}
	}
}

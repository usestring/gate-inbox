package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// requeuedNote follows a held prompt's reason when the prompt was queued to
// go in once the turn may start.
const requeuedNote = " Your prompt is queued and goes in once the session can take it."

// TurnGateHook is the prompt-submit hook's turn gate: it asks the build's
// turn gates whether the session may start this turn, and blocks the prompt
// when one holds it. A gate that asks for the prompt to be requeued has it
// queued as the operator's words, so the board types it in once the gates
// let a turn through; only a prompt the operator typed is requeued, never a
// message another session sent, which must not come back in the operator's
// voice. Nothing held, or no session, prints nothing.
func TurnGateHook(configDir, sessionID string, payload []byte, now time.Time) string {
	if sessionID == "" {
		return ""
	}
	var event struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(payload, &event)
	path := filepath.Join(configDir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	st, err := store.Open(path)
	if err != nil {
		return ""
	}
	defer st.Close()
	sess, err := st.Get(sessionID)
	if err != nil {
		return ""
	}
	verdict := sessionhooks.GateTurn(sess, extension.TurnFromPrompt, 0)
	if !verdict.Hold {
		return ""
	}
	reason := verdict.Reason
	if verdict.Requeue && typedByOperator(event.Prompt) {
		_, _, err := st.Enqueue(store.InboxMessage{
			SessionID:   sessionID,
			SenderID:    store.RelayedHumanSenderID,
			SenderName:  "operator",
			Body:        event.Prompt,
			Fingerprint: textfmt.Fingerprint(event.Prompt + "\n" + now.UTC().Format(time.RFC3339Nano)),
			SentAt:      now,
		}, store.DefaultInboxLimits)
		if err == nil {
			reason += requeuedNote
		}
	}
	return hookJSON(map[string]string{"decision": "block", "reason": reason})
}

// typedByOperator says a prompt carries neither a seal nor a cross-session
// fence: it is not a message the board delivered from another session.
func typedByOperator(prompt string) bool {
	if strings.TrimSpace(prompt) == "" || strings.Contains(prompt, crossSessionFence) {
		return false
	}
	_, _, _, sealed := parentseal.Split(prompt)
	return !sealed
}

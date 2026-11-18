package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
)

type fakePRCommands struct {
	owner    sessioncmd.PROwnership
	delivery store.PRDelivery
	err      error
	args     []string
}

func (f *fakePRCommands) PROwner(url string) (sessioncmd.PROwnership, error) {
	f.args = []string{url}
	return f.owner, f.err
}
func (f *fakePRCommands) PRFeedback(url, key, id, body string) (store.PRDelivery, error) {
	f.args = []string{url, key, id, body}
	return f.delivery, f.err
}

func TestPROwnerCLIContract(t *testing.T) {
	for _, state := range []string{"owned", "none", "ambiguous"} {
		t.Run(state, func(t *testing.T) {
			fake := &fakePRCommands{owner: sessioncmd.PROwnership{Status: state, URL: "https://github.com/o/r/pull/1"}}
			if state == "owned" {
				fake.owner.SessionID = "worker"
			}
			if state == "ambiguous" {
				fake.owner.SessionIDs = []string{"a", "b"}
			}
			var out bytes.Buffer
			if err := runPROwner(&out, fake, []string{fake.owner.URL, "--json"}, ""); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["status"] != state || got["url"] != fake.owner.URL {
				t.Fatalf("JSON=%v", got)
			}
			if state == "owned" && got["session_id"] != "worker" {
				t.Fatalf("flat owner missing: %v", got)
			}
		})
	}
	var out bytes.Buffer
	if err := runPROwner(&out, &fakePRCommands{err: errors.New("unavailable")}, []string{"url", "--json"}, ""); err == nil || out.Len() != 0 {
		t.Fatalf("error emitted fallback JSON: %q,%v", out.String(), err)
	}
}

func TestPRFeedbackCLIReceiptAndBoundedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.txt")
	if err := os.WriteFile(path, []byte("feedback\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &fakePRCommands{delivery: store.PRDelivery{Status: "accepted", URL: "https://github.com/o/r/pull/1", EventKey: "hash", MessageID: 42, SessionID: "worker"}}
	args := []string{fake.delivery.URL, "--event-key", "hash", "--expect-session-id", "worker", "--message-file", path, "--json"}
	var out bytes.Buffer
	if err := runPRFeedback(&out, fake, args, ""); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["message_id"] != float64(42) || got["session_id"] != "worker" || got["event_key"] != "hash" || got["url"] != fake.delivery.URL || got["status"] != "accepted" || fake.args[3] != "feedback\n" {
		t.Fatalf("receipt=%v,args=%q", got, fake.args)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 8001)), 0600); err != nil {
		t.Fatal(err)
	}
	fake.args = nil
	if err := runPRFeedback(&out, fake, args, ""); err == nil || fake.args != nil {
		t.Fatal("oversized file reached queue")
	}
	if err := runPRFeedback(&out, fake, []string{fake.delivery.URL, "--json"}, ""); err == nil {
		t.Fatal("missing guards accepted")
	}
}

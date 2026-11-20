package migrate

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tooldrivers/tooldriverstest"
)

func TestLocateAsksTheDriverNamedBySessionStore(t *testing.T) {
	var got extension.TranscriptRequest
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name: "fake",
		Transcript: func(req extension.TranscriptRequest) (extension.Transcript, error) {
			got = req
			return extension.Transcript{Path: "/store/" + req.ID + ".jsonl", Format: "one message per line"}, nil
		},
	})
	source := store.Session{ID: "s1", Name: "app", Cwd: "/repo", AgentSessionID: "c0ffee"}
	transcript, err := Locate(fixtureRoots(t), "mine", config.Tool{Command: "fake-cli", SessionStore: "fake"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Path != "/store/c0ffee.jsonl" || transcript.Format != "one message per line" || transcript.Kind != "" {
		t.Fatalf("transcript = %+v", transcript)
	}
	if got.ID != "c0ffee" || got.Directory != "/repo" {
		t.Fatalf("request = %+v", got)
	}
}

func TestLocateRefusesADriverThatCannotHandOver(t *testing.T) {
	tooldriverstest.Install(t,
		&tooldriverstest.Driver{Name: "mute"},
		&tooldriverstest.Driver{Name: "both", Transcript: func(extension.TranscriptRequest) (extension.Transcript, error) {
			return extension.Transcript{Path: "/a", Command: "cat /a"}, nil
		}},
	)
	source := store.Session{ID: "s1", Name: "app", Cwd: "/repo", AgentSessionID: "c0ffee"}
	if _, err := Locate(fixtureRoots(t), "mine", config.Tool{SessionStore: "mute"}, source); err == nil || !strings.Contains(err.Error(), "cannot locate") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Locate(fixtureRoots(t), "mine", config.Tool{SessionStore: "both"}, source); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("err = %v", err)
	}
}

// pi's session JSONL is a layout the core's handover filter reads, so a pi
// driver's transcript carries Kind "pi" and is filtered; a command never is.
func TestLocateGivesAPiDriversFileThePiKind(t *testing.T) {
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name: "pi",
		Transcript: func(req extension.TranscriptRequest) (extension.Transcript, error) {
			return extension.Transcript{Path: "/sessions/--repo--/x_" + req.ID + ".jsonl", Format: "pi session JSONL"}, nil
		},
	})
	source := store.Session{ID: "s1", Name: "app", Cwd: "/repo", AgentSessionID: "c0ffee"}
	transcript, err := Locate(fixtureRoots(t), "pi", config.Tool{Command: "pi", SessionStore: "pi"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Kind != "pi" || transcript.Format != "pi session JSONL" {
		t.Fatalf("transcript = %+v", transcript)
	}
}

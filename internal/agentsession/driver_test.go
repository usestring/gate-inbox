package agentsession

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/tooldrivers/tooldriverstest"
)

func TestCaptureAsksTheDriverNamedByTheStore(t *testing.T) {
	launched := time.Now()
	var got extension.CaptureRequest
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name: "fake",
		Capture: func(req extension.CaptureRequest) (string, error) {
			got = req
			if req.Claimed("taken-1") {
				return "fresh-2", nil
			}
			return "taken-1", nil
		},
	})
	id, ok := Capture("fake", "/repo", launched, map[string]bool{"taken-1": true})
	if !ok || id != "fresh-2" {
		t.Fatalf("Capture = %q, %v", id, ok)
	}
	if got.Directory != "/repo" || !got.LaunchedAt.Equal(launched) {
		t.Fatalf("request = %+v", got)
	}
}

func TestCaptureDropsADriverIdThatIsClaimedOrMalformed(t *testing.T) {
	answer := ""
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name:    "fake",
		Capture: func(extension.CaptureRequest) (string, error) { return answer, nil },
	})
	for _, bad := range []string{"", "taken", "$(rm -rf ~)", "-flag"} {
		answer = bad
		if id, ok := Capture("fake", "/repo", time.Now(), map[string]bool{"taken": true}); ok {
			t.Fatalf("captured %q from a driver answering %q", id, bad)
		}
	}
}

func TestSessionFileAsksTheDriver(t *testing.T) {
	tooldriverstest.Install(t,
		&tooldriverstest.Driver{Name: "filed", File: func(id string) (string, error) { return "/store/" + id + ".json", nil }},
		&tooldriverstest.Driver{Name: "unfiled"},
	)
	if path, err := SessionFile("filed", "abc"); err != nil || path != "/store/abc.json" {
		t.Fatalf("SessionFile = %q, %v", path, err)
	}
	if _, err := SessionFile("unfiled", "abc"); err == nil || !strings.Contains(err.Error(), "keeps no conversation in a file") {
		t.Fatalf("err = %v", err)
	}
	if _, err := SessionFile("codex", "abc"); err == nil {
		t.Fatal("a built-in store has no session file")
	}
}

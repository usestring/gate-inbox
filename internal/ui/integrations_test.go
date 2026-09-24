package ui

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type countingTransport struct{ requests int }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.requests++
	return nil, errors.New("no network in tests")
}

// outbound counts what the board's real providers try to reach: a gh on PATH that records each
// run, and the default HTTP transport Linear's client falls through to.
type outbound struct {
	ghLog string
	http  *countingTransport
}

func (o outbound) ghCalls(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(o.ghLog)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func watchOutbound(t *testing.T, linearKey string) outbound {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "gh.log")
	script := "#!/bin/sh\necho called >> " + log + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LINEAR_API_KEY", linearKey)
	transport := &countingTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	return outbound{ghLog: log, http: transport}
}

func switched(github, linear bool) config.Config {
	var cfg config.Config
	cfg.Integrations.GitHub.Enabled = &github
	cfg.Integrations.Linear.Enabled = &linear
	return cfg
}

func integrationModel(t *testing.T, cfg config.Config) *Model {
	t.Helper()
	m := &Model{
		width:  100,
		height: 30,
		work:   newWorkTracker(cfg, nil),
		sessions: []store.Session{{
			ID: "s1", Name: "work", Tool: "claude", Status: status.Waiting, Cwd: t.TempDir(),
			LaunchPrompt: "fix ABC-4242\nhttps://github.com/example-org/sample-repo/pull/7394\n",
			CreatedAt:    time.Now(),
		}},
	}
	m.runWork(t)
	return m
}

// Providers switched off cost nothing: no resolver, no gh run, no request, no rail row, and health
// that reads as off rather than failing.
func TestSwitchedOffProvidersReachNothing(t *testing.T) {
	cases := []struct {
		name           string
		github, linear bool
		key            string
	}{
		{"both off", false, false, "key"},
		{"both off without a key", false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := watchOutbound(t, c.key)
			m := integrationModel(t, switched(c.github, c.linear))

			if m.work.GitHub != nil || m.work.Linear != nil {
				t.Errorf("resolvers built: github=%v linear=%v", m.work.GitHub, m.work.Linear)
			}
			if n := out.ghCalls(t); n != 0 {
				t.Errorf("gh ran %d times", n)
			}
			if out.http.requests != 0 {
				t.Errorf("%d HTTP requests", out.http.requests)
			}
			if rows := m.workRowsFor("s1"); len(rows) != 0 {
				t.Errorf("rail rows drawn: %+v", rows)
			}
			gh, ln := m.work.Health()
			if !gh.Off || gh.Failed() || !ln.Off || ln.Failed() {
				t.Errorf("health github=%+v linear=%+v, want both off and not failed", gh, ln)
			}
		})
	}
}

// The control for the test above: the same board with both on does reach both, so a zero there
// is the switch and not a harness that cannot see a call.
func TestSwitchedOnProvidersAreAsked(t *testing.T) {
	out := watchOutbound(t, "key")
	m := integrationModel(t, switched(true, true))

	if out.ghCalls(t) == 0 {
		t.Error("gh never ran with GitHub on")
	}
	if out.http.requests == 0 {
		t.Error("no request reached Linear with Linear on and a key")
	}
	if rows := m.workRowsFor("s1"); len(rows) == 0 {
		t.Error("no rail rows for references still waiting on a look")
	}
}

// Linear on without a key still draws its tickets, with no state, beside GitHub's rows. It sends
// nothing, and its health names the missing key instead of reading as a failure.
func TestKeylessLinearDrawsTicketsWithoutState(t *testing.T) {
	out := watchOutbound(t, "")
	m := integrationModel(t, switched(false, true))

	if m.work.Linear == nil {
		t.Fatal("no ticket resolver with Linear on and no key")
	}
	if out.http.requests != 0 {
		t.Errorf("%d HTTP requests with no key", out.http.requests)
	}
	var tickets int
	for _, row := range m.workRowsFor("s1") {
		if row.kind != "TICKET" {
			continue
		}
		tickets++
		if row.label != "ABC-4242" || row.detail != "not looked up" {
			t.Errorf("ticket row = %+v, want ABC-4242 with no state", row)
		}
	}
	if tickets != 1 {
		t.Errorf("%d ticket rows with Linear keyless, want 1", tickets)
	}
	_, ln := m.work.Health()
	if !ln.Off || ln.Failed() || !strings.Contains(ln.Reason, "LINEAR_API_KEY is not set") {
		t.Errorf("linear health = %+v, want off, not failed, naming the missing key", ln)
	}
}

// Linear switched off in the config removes its tickets even with a key: no row, no request.
func TestLinearSwitchedOffDrawsNoTickets(t *testing.T) {
	out := watchOutbound(t, "key")
	m := integrationModel(t, switched(true, false))

	if m.work.Linear != nil {
		t.Error("ticket resolver built with Linear switched off")
	}
	if out.http.requests != 0 {
		t.Errorf("%d HTTP requests with Linear off", out.http.requests)
	}
	rows := m.workRowsFor("s1")
	for _, row := range rows {
		if row.kind == "TICKET" {
			t.Errorf("ticket row with Linear off: %+v", row)
		}
	}
	if len(rows) == 0 {
		t.Error("GitHub's rows went with Linear")
	}
}

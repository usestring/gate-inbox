package sessioncmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

type classifierTransport func(*http.Request) (*http.Response, error)

func (f classifierTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLaunchClassifierUsesSavedProviderAndOwnKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("WORKERS_API_KEY", "")
	t.Setenv("WORKERS_AI_URL", "https://worker.example")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Sessions{configDir: dir, claudeHome: t.TempDir()}
	tr := &transcript{}
	tr.add(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Launch a new session to check the parser"}})
	tr.write(t, s.claudeHome, "conv-parent")
	caller := store.Session{ID: "parent01", Tool: "claude", AgentSessionID: "conv-parent"}
	runtime := &runtime{store: st, cfg: config.Config{}}
	previous := classifier.HTTPClient
	t.Cleanup(func() { classifier.HTTPClient = previous })
	for _, provider := range []classifier.Provider{classifier.JEV, classifier.Clef} {
		t.Run(provider.Name(), func(t *testing.T) {
			if err := st.SetSetting(classifier.ProviderSetting, string(provider)); err != nil {
				t.Fatal(err)
			}
			path := provider.KeyPath(dir)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			key := "fixture-" + provider.Name()
			if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			body := `{"model":"jev-1.13.0","answers":{"asked_launch":{"type":"noul","noul":0.95}}}`
			if provider == classifier.Clef {
				body = `[{"model":"clef","answers":{"asked_launch":{"type":"noul","noul":0.95}}}]`
			}
			classifier.HTTPClient = &http.Client{Transport: classifierTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != provider.Endpoint() {
					t.Errorf("request destination: %s", r.URL)
				}
				if provider == classifier.Clef {
					if r.Header.Get("X-Authorization") != key || r.Header.Get("Authorization") != "" {
						t.Error("Clef did not use its own saved key")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("X-Authorization") != "" {
					t.Error("JEV did not use its own saved key")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			if !s.jevAskedLaunch(runtime, caller) || calls != 1 {
				t.Fatalf("configured launch classification: calls=%d", calls)
			}
			body = `{}`
			if s.jevAskedLaunch(runtime, caller) {
				t.Error("invalid answer bypassed manual confirmation")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			calls = 0
			if s.jevAskedLaunch(runtime, caller) || calls != 0 {
				t.Errorf("missing key should require confirmation without a request: calls=%d", calls)
			}
		})
	}
}

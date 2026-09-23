package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

func TestContextSnapshotsDeduplicateAndUseExactConversation(t *testing.T) {
	st, _ := routingStore(t)
	reader := promptcache.NewReader(t.TempDir())
	dir := reader.ProjectDir("/fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, session := range []store.Session{
		{ID: "live", Tool: "claude", Cwd: "/fixture", AgentSessionID: "exact", Account: "LENDER"},
		{ID: "unknown", Tool: "claude", Cwd: "/fixture", Account: "LENDER"},
		{ID: "dead", Tool: "claude", Cwd: "/fixture", AgentSessionID: "exact", Account: "LENDER"},
		{ID: "other-tool", Tool: "codex", Cwd: "/fixture", AgentSessionID: "exact", Account: "LENDER"},
	} {
		session.CreatedAt = time.Now().Add(-2 * time.Minute)
		if err := st.CreateSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting("account_borrower:live", "ORIGINAL_OWNER"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(store.DefaultAccountSetting, "NEW_OWNER"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
	write := func(name string, output int) {
		t.Helper()
		body := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-test","usage":{"input_tokens":40,"output_tokens":%d,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}}}`, at, output)
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("exact", 50)
	write("newest-unrelated", 999)
	capture := tracetest.Capture(t)
	tools := map[string]config.Tool{"claude": {AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}}
	last := map[string]contextSnapshot{}
	running := func(id string) bool { return id != "dead" }
	collect := func() {
		t.Helper()
		if err := recordSessionUsage(st, tools, running, reader, last); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	collect()
	write("exact", 80)
	collect()
	collect()
	if err := st.SetAgentLaunchedAt("live", time.Now().Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	write("exact", 90)
	collect()
	at = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	write("exact", 100)
	collect()
	spans := tracetest.Named(capture(), "account.context")
	if len(spans) != 3 {
		t.Fatalf("got %d context snapshots, want three changed samples from the current launch", len(spans))
	}
	for i, span := range spans {
		for key, want := range map[string]any{"session": "live", "account.borrower": "ORIGINAL_OWNER", "account.lender": "LENDER", "account.attributed": true, "model": "claude-test", "usage.context_tokens": int64(1240), "usage.input_tokens": int64(40), "usage.cache_read_input_tokens": int64(1000), "usage.cache_creation_input_tokens": int64(200)} {
			if span.Attr(key) != want {
				t.Errorf("%s = %v, want %v", key, span.Attr(key), want)
			}
		}
		if span.Attr("usage.output_tokens") != []int64{50, 80, 100}[i] {
			t.Errorf("output count = %v", span.Attr("usage.output_tokens"))
		}
	}
	running = func(string) bool { return false }
	collect()
	if len(last) != 0 {
		t.Fatal("stopped sessions retained in monitor")
	}
}

func TestOwnLoginContextDoesNotInventAccountAttribution(t *testing.T) {
	st, _ := routingStore(t)
	reader := promptcache.NewReader(t.TempDir())
	dir := reader.ProjectDir("/fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(store.Session{ID: "own", Tool: "claude", Cwd: "/fixture", AgentSessionID: "uncached", CreatedAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"usage":{"input_tokens":25,"output_tokens":5}}}`, time.Now().Add(-time.Second).Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, "uncached.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := tracetest.Capture(t)
	if err := recordSessionUsage(st, map[string]config.Tool{"claude": {AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}}, func(string) bool { return true }, reader, map[string]contextSnapshot{}); err != nil {
		t.Fatal(err)
	}
	span := tracetest.One(t, capture(), "account.context")
	if span.Attr("usage.context_tokens") != int64(25) || span.Attr("account.attributed") != false || span.Attr("account.lender") != "" {
		t.Fatalf("incorrect local-login snapshot: %+v", span.Attrs)
	}
}

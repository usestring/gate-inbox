package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type routingTransport func(*http.Request) (*http.Response, error)

func (f routingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSmartRoutingLaunchAndHTTPExportEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the real one-minute usage monitor")
	}
	h := newRoutingHarness(t)
	parentClient := connect(t, h.configDir, h.caller.ID)
	for key, value := range map[string]string{store.DefaultAccountSetting: "WRONG_LEGACY_OWNER", store.AccountRoutingSetting: "smart"} {
		if err := h.store.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	var exhausted atomic.Bool
	h.pool.UsageOf = func(account string) (extension.AccountUsage, error) {
		used := 10.0
		if account == "OWNER" && exhausted.Load() {
			used = 100
		}
		return accountstest.Quota(time.Now(), used, time.Hour), nil
	}
	payloads := make(chan []byte, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/traces":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-axiom-token" || r.Header.Get("X-Axiom-Dataset") != "fixture-routing" {
				t.Error("trace request lost its destination or authentication headers")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			payloads <- body
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected HTTP path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = routingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.axiom.co" {
			return nil, fmt.Errorf("test refuses external host %q", r.URL.Host)
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = local.Scheme, local.Host
		return originalTransport.RoundTrip(clone)
	})
	defer func() { http.DefaultTransport = originalTransport }()
	t.Setenv(tracing.TokenEnv, "fixture-axiom-token")
	t.Setenv(tracing.DatasetEnv, "fixture-routing")
	tracer, err := tracing.Start("axiom")
	if err != nil {
		t.Fatal(err)
	}
	defer tracer.Close()
	var lastSession, borrowedChild string
	for i, want := range []string{"OWNER", "ALICE1", "POOL2", "OWNER", ""} {
		if i == 1 {
			exhausted.Store(true)
			if err := h.store.SetSetting("account_usage:v2:CLAUDE_OAUTH_TOKEN_{account}:OWNER", ""); err != nil {
				t.Fatal(err)
			}
		}
		client := parentClient
		args := map[string]any{"tool": "claude", "name": "routing-fixture", "prompt": "fixture-private-prompt"}
		if i == 2 {
			client = connect(t, h.configDir, borrowedChild)
		}
		if i == 3 {
			args["account"] = "OWNER"
		}
		if i == 4 {
			if err := h.store.SetSetting(store.AccountRoutingSetting, "own"); err != nil {
				t.Fatal(err)
			}
		}
		result := callTool(t, client, "create_session", args)
		if result.IsError {
			data, _ := json.Marshal(result.Content)
			t.Fatalf("MCP create_session failed: %s", data)
		}
		var created sessioncmd.Session
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &created); err != nil {
			t.Fatal(err)
		}
		if created.ParentID != h.caller.ID {
			t.Fatalf("child lost parent: %+v", created)
		}
		if i == 1 {
			borrowedChild = created.ID
		}
		if created.Account != want {
			t.Fatalf("launch %d account = %q, want %q", i, created.Account, want)
		}
		if i == 2 {
			lastSession = created.ID
		}
		output := "test-token-CLAUDE_OAUTH_TOKEN_" + want
		if want == "" {
			output = "fixture-local-login"
		}
		waitForRoutingOutput(t, h.sessions, h.caller.ID, created.ID, output)
		borrower, err := h.store.Setting("account_borrower:" + created.ID)
		if err != nil || borrower != "OWNER" {
			t.Fatalf("borrower = %q, err = %v", borrower, err)
		}
	}
	if reads := h.pool.Reads(); reads != 4 {
		t.Errorf("quota reads = %d, want one per account plus the invalidated owner", reads)
	}
	t.Setenv("HOME", t.TempDir())
	row, err := h.store.Get(lastSession)
	if err != nil || row.AgentSessionID == "" {
		t.Fatalf("missing conversation identity: %+v, %v", row, err)
	}
	reader := promptcache.NewReader(promptcache.DefaultRoot())
	dir := reader.ProjectDir(row.Cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fixture","content":"fixture-private-response","usage":{"input_tokens":500,"output_tokens":75,"cache_read_input_tokens":4000,"cache_creation_input_tokens":1000}}}`, time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, row.AgentSessionID+".jsonl"), []byte(fixture+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadDir(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	stop := accounts.StartUsageMonitor(h.store, cfg.Tools, h.driver.Exists)
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	timer := time.NewTimer(70 * time.Second)
	defer timer.Stop()
	var received [][]byte
	foundContext := false
	for !foundContext {
		select {
		case body := <-payloads:
			received = append(received, body)
			foundContext = strings.Contains(string(body), `"name":"account.context"`)
		case <-timer.C:
			t.Fatal("minute monitor did not export context to the HTTP receiver")
		}
	}
	stop()
	stopped = true
	if err := tracer.Close(); err != nil {
		t.Fatal(err)
	}
	close(payloads)
	for body := range payloads {
		received = append(received, body)
	}
	counts := map[string]int{}
	for _, body := range received {
		for _, forbidden := range []string{"fixture-private-prompt", "fixture-private-response", "test-token-", "fixture-axiom-token"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("export leaked fixture content or credentials")
			}
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						Name       string `json:"name"`
						Attributes []struct {
							Key   string         `json:"key"`
							Value map[string]any `json:"value"`
						} `json:"attributes"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		for _, resource := range payload.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					counts[span.Name]++
					if span.Name == "account.context" {
						attrs := map[string]any{}
						for _, attr := range span.Attributes {
							for _, value := range attr.Value {
								attrs[attr.Key] = value
							}
						}
						for key, want := range map[string]any{
							"session": lastSession, "model": "claude-fixture", "account.borrower": "OWNER", "account.lender": "POOL2",
							"usage.context_tokens": "5500", "usage.input_tokens": "500", "usage.output_tokens": "75",
							"usage.cache_read_input_tokens": "4000", "usage.cache_creation_input_tokens": "1000",
						} {
							if attrs[key] != want {
								t.Errorf("context %s = %v, want %v", key, attrs[key], want)
							}
						}
					}
				}
			}
		}
	}
	for name, want := range map[string]int{"account.selection": 5, "account.launch": 4, "account.context": 1} {
		if counts[name] != want {
			t.Errorf("received %d %s spans, want %d", counts[name], name, want)
		}
	}
	if counts["account.usage"] < 8 {
		t.Errorf("received %d quota-window spans, want at least 8", counts["account.usage"])
	}
}

type routingHarness struct {
	configDir string
	pool      *accountstest.Pool
	driver    *tmux.Driver
	store     *store.Store
	sessions  *sessioncmd.Sessions
	caller    store.Session
}

func newRoutingHarness(t *testing.T) *routingHarness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	pool := accountstest.LoggedInAs("owner")
	t.Cleanup(accounts.UsePool(pool.Resolve))
	configDir := tmuxtest.ScratchDir(t)
	t.Setenv(config.HomeEnv, configDir)
	socket := tmuxtest.NewSocket("routing")
	t.Setenv(tmux.SocketEnv, socket)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-parent-borrowed-token")
	text := `[tools.claude]
command = "sh -c 'if [ -n \"$CLAUDE_CODE_OAUTH_TOKEN\" ]; then printenv CLAUDE_CODE_OAUTH_TOKEN; else echo fixture-local-login; fi'; echo"
default_status = "idle"
account_env = "CLAUDE_CODE_OAUTH_TOKEN"
account_secret = "CLAUDE_OAUTH_TOKEN_{account}"
account_command = "echo test-token-{secret}"
accounts_command = "printf 'CLAUDE_OAUTH_TOKEN_OWNER\\nCLAUDE_OAUTH_TOKEN_ALICE1\\nCLAUDE_OAUTH_TOKEN_POOL2\\n'"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	caller := store.Session{ID: "abcdef01", Name: "routing-parent", Tool: "claude", Cwd: t.TempDir(), Status: status.Idle, Account: "POOL2"}
	if err := driver.Create(caller.ID, caller.Cwd, "", nil, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(caller); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		driver.CloseCaptureClients()
		if !tmux.OwnsSocket(socket) {
			t.Fatalf("refusing cleanup of non-test socket %q", socket)
		}
		exec.Command("tmux", "-L", socket, "kill-server").Run()
		tmuxtest.ReapSocket(socket)
		st.Close()
	})
	return &routingHarness{configDir: configDir, pool: pool, driver: driver, store: st, caller: caller, sessions: sessioncmd.NewSessions(configDir, sessioncmd.MCPVocabulary())}
}

func waitForRoutingOutput(t *testing.T, sessions *sessioncmd.Sessions, caller, child, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		screen, err := sessions.Read(caller, child, "")
		if err == nil && strings.Contains(screen.Output, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("child %s did not launch with selected account %s", child, want)
}

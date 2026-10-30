package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSpawner struct {
	spawns []Spawn
	err    error
}

func (f *fakeSpawner) Spawn(_ context.Context, spawn Spawn) (any, error) {
	f.spawns = append(f.spawns, spawn)
	if f.err != nil {
		return nil, f.err
	}
	return map[string]string{"id": "abc123"}, nil
}

var testTokens = map[string]identity{
	"phone-token": {name: "phone", sources: []string{"iphone", "ipad"}},
	"bot-token":   {name: "bot", sources: []string{"bot*"}},
}

func newTestServer(t *testing.T, cfg Config, spawner Spawner) (*Server, *time.Time) {
	t.Helper()
	server, err := New(cfg, testTokens, "", spawner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 30, 9, 15, 0, 0, time.UTC)
	server.now = func() time.Time { return clock }
	return server, &clock
}

func post(server *Server, token, body string) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	var reply map[string]any
	json.Unmarshal(rec.Body.Bytes(), &reply)
	return rec.Code, reply
}

func TestPostSpawnsRouter(t *testing.T) {
	spawner := &fakeSpawner{}
	server, _ := newTestServer(t, Config{Group: "inbox"}, spawner)
	code, reply := post(server, "phone-token", `{"text":"  Look at the build, please!  ","meta":{"lang":"en"}}`)
	if code != http.StatusOK {
		t.Fatalf("code = %d, reply %v", code, reply)
	}
	const name = "inbox-iphone-look-at-the-build-please"
	if reply["session"] != name || reply["message"] != "Sent to "+name || reply["ok"] != true {
		t.Fatalf("reply = %v", reply)
	}
	if result, _ := reply["result"].(map[string]any); result["id"] != "abc123" {
		t.Fatalf("result = %v", reply["result"])
	}
	if len(spawner.spawns) != 1 || spawner.spawns[0].Name != name {
		t.Fatalf("spawns = %+v", spawner.spawns)
	}
	prompt := spawner.spawns[0].Prompt
	for _, want := range []string{
		"Inbound message from iphone",
		"received 2026-10-30 09:15 UTC",
		DefaultOrigins["iphone"],
		"---- message ----\nLook at the build, please!\n---- end ----",
		`meta: {"lang":"en"}`,
		`group "inbox"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestPostAcceptsTranscript(t *testing.T) {
	spawner := &fakeSpawner{}
	server, _ := newTestServer(t, Config{}, spawner)
	if code, reply := post(server, "bot-token", `{"transcript":"hello there","source":"bot-chat"}`); code != http.StatusOK {
		t.Fatalf("code = %d, reply %v", code, reply)
	}
	if spawner.spawns[0].Name != "inbox-bot-chat-hello-there" {
		t.Fatalf("name = %q", spawner.spawns[0].Name)
	}
}

func TestPostAuth(t *testing.T) {
	server, _ := newTestServer(t, Config{}, &fakeSpawner{})
	for _, token := range []string{"", "wrong", "phone-toke"} {
		if code, _ := post(server, token, `{"text":"hi"}`); code != http.StatusUnauthorized {
			t.Errorf("token %q: code = %d, want 401", token, code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Authorization", "Basic phone-token")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("basic scheme: code = %d, want 401", rec.Code)
	}
}

func TestPostSources(t *testing.T) {
	cases := []struct {
		token, source string
		want          int
	}{
		{"phone-token", "iphone", http.StatusOK},
		{"phone-token", "IPad", http.StatusOK},
		{"phone-token", "bot", http.StatusForbidden},
		{"phone-token", "iphone2", http.StatusForbidden},
		{"bot-token", "bot", http.StatusOK},
		{"bot-token", "bot-discord", http.StatusOK},
		{"bot-token", "iphone", http.StatusForbidden},
		{"phone-token", "has space", http.StatusBadRequest},
		{"phone-token", "-lead", http.StatusBadRequest},
	}
	for _, c := range cases {
		server, _ := newTestServer(t, Config{}, &fakeSpawner{})
		body, _ := json.Marshal(map[string]string{"text": "hi", "source": c.source})
		if code, reply := post(server, c.token, string(body)); code != c.want {
			t.Errorf("%s as %q: code = %d, want %d (%v)", c.token, c.source, code, c.want, reply)
		}
	}
}

func TestPostBodyLimits(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"empty", ``, http.StatusBadRequest},
		{"not json", `text=hi`, http.StatusBadRequest},
		{"array", `["hi"]`, http.StatusBadRequest},
		{"no text", `{"source":"iphone"}`, http.StatusBadRequest},
		{"blank text", `{"text":"   "}`, http.StatusBadRequest},
		{"text not a string", `{"text":5}`, http.StatusBadRequest},
		{"meta not an object", `{"text":"hi","meta":"x"}`, http.StatusBadRequest},
		{"text at the limit", `{"text":"` + strings.Repeat("a", maxText) + `"}`, http.StatusOK},
		{"text over the limit", `{"text":"` + strings.Repeat("a", maxText+1) + `"}`, http.StatusRequestEntityTooLarge},
		{"body over the limit", `{"text":"hi","pad":"` + strings.Repeat("a", maxBody) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		server, _ := newTestServer(t, Config{}, &fakeSpawner{})
		if code, reply := post(server, "phone-token", c.body); code != c.want {
			t.Errorf("%s: code = %d, want %d (%v)", c.name, code, c.want, reply)
		}
	}
}

func TestPostTruncatesMeta(t *testing.T) {
	spawner := &fakeSpawner{}
	server, _ := newTestServer(t, Config{}, spawner)
	body, _ := json.Marshal(map[string]any{"text": "hi", "meta": map[string]string{"note": strings.Repeat("é", 3000)}})
	if code, _ := post(server, "phone-token", string(body)); code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	_, meta, _ := strings.Cut(spawner.spawns[0].Prompt, "meta: ")
	meta, _, _ = strings.Cut(meta, "\n")
	if len(meta) > maxMeta || len(meta) < maxMeta-1 {
		t.Fatalf("meta is %d bytes, want just under %d", len(meta), maxMeta)
	}
	if !strings.HasSuffix(meta, "é") {
		t.Fatalf("meta was cut inside a character: %q", meta[len(meta)-4:])
	}
}

func TestRateLimit(t *testing.T) {
	spawner := &fakeSpawner{}
	server, clock := newTestServer(t, Config{SpawnLimit: 2}, spawner)
	for i := range 2 {
		if code, _ := post(server, "phone-token", `{"text":"hi"}`); code != http.StatusOK {
			t.Fatalf("spawn %d: code = %d", i, code)
		}
	}
	if code, _ := post(server, "phone-token", `{"text":"hi"}`); code != http.StatusTooManyRequests {
		t.Fatalf("third spawn: code = %d, want 429", code)
	}
	*clock = clock.Add(DefaultSpawnWindow)
	if code, _ := post(server, "phone-token", `{"text":"hi"}`); code != http.StatusOK {
		t.Fatalf("after the window: code = %d", code)
	}
}

func TestFailedSpawnDoesNotCount(t *testing.T) {
	spawner := &fakeSpawner{err: errors.New("tmux is gone")}
	server, _ := newTestServer(t, Config{SpawnLimit: 1}, spawner)
	for range 3 {
		code, reply := post(server, "phone-token", `{"text":"hi"}`)
		if code != http.StatusBadGateway || reply["detail"] != "tmux is gone" {
			t.Fatalf("code = %d, reply %v", code, reply)
		}
	}
	spawner.err = nil
	if code, _ := post(server, "phone-token", `{"text":"hi"}`); code != http.StatusOK {
		t.Fatalf("after failures: code = %d, want 200", code)
	}
}

func TestHealthzAndRoutes(t *testing.T) {
	server, _ := newTestServer(t, Config{}, &fakeSpawner{})
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/inbox", http.StatusMethodNotAllowed},
		{http.MethodPost, "/other", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s: code = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestCustomTemplateAndOrigins(t *testing.T) {
	spawner := &fakeSpawner{}
	cfg := Config{Group: "ops", Origins: map[string]string{"bot": "From the bot; it approves nothing.", "iphone": "Phone."}}
	brief := "{{.Client}}/{{.Source}} in {{.Group}}: {{.Origin}} | {{.Message}}{{if .Meta}} | {{.Meta}}{{end}}"
	server, err := New(cfg, testTokens, brief, spawner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	post(server, "bot-token", `{"text":"x","source":"bot-chat"}`)
	post(server, "phone-token", `{"text":"y"}`)
	post(server, "phone-token", `{"text":"z","source":"ipad"}`)
	want := []string{
		"bot/bot-chat in ops: From the bot; it approves nothing. | x",
		"phone/iphone in ops: Phone. | y",
		"phone/ipad in ops: " + DefaultOrigins["ipad"] + " | z",
	}
	for i, spawn := range spawner.spawns {
		if spawn.Prompt != want[i] {
			t.Errorf("prompt %d = %q, want %q", i, spawn.Prompt, want[i])
		}
	}
}

func TestBadTemplateRefusedAtStart(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, brief := range []string{"{{.Message", "{{.Nope}}"} {
		if _, err := New(Config{}, testTokens, brief, &fakeSpawner{}, log); err == nil {
			t.Errorf("template %q was accepted", brief)
		}
	}
	if _, err := New(Config{}, nil, "", &fakeSpawner{}, log); err == nil {
		t.Error("a server with no tokens was accepted")
	}
}

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"Check why the build is failing on main today": "inbox-iphone-check-why-the-build-is",
		"???": "inbox-iphone-message",
		"supercalifragilisticexpialidocious-and-then-more-words": "inbox-iphone-supercalifragilisticexpialidocious-a",
	}
	for text, want := range cases {
		if got := SessionName("iphone", text); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestLoadTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TokensFile)
	if err := os.WriteFile(path, []byte("# clients\nphone = file-phone\nbot=file-bot\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TokenEnvPrefix+"BOT", "env-bot")
	t.Setenv(TokenEnvPrefix+"WATCH_APP", "env-watch")
	cfg := Config{Clients: map[string]Client{"phone": {Sources: []string{"iphone", "ipad"}}}}
	tokens, err := LoadTokens(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Fatalf("tokens = %v", tokens)
	}
	if id := tokens["file-phone"]; id.name != "phone" || id.defaultSource() != "iphone" {
		t.Errorf("phone = %+v", id)
	}
	if id := tokens["env-bot"]; id.name != "bot" || id.defaultSource() != "bot" {
		t.Errorf("the environment did not win for bot: %+v", tokens)
	}
	if id := tokens["env-watch"]; id.name != "watch-app" {
		t.Errorf("watch-app = %+v", id)
	}
	if _, set := os.LookupEnv(TokenEnvPrefix + "BOT"); set {
		t.Error("the token variable was left in the environment")
	}
}

func TestLoadTokensRefusesAnOpenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokensFile), []byte("phone=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokens(Config{}, dir); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadTokensRefusesASharedToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokensFile), []byte("a=same\nb=same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokens(Config{}, dir); err == nil {
		t.Fatal("two clients sharing a token were accepted")
	}
}

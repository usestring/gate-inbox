package inbox

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode/utf8"
)

const (
	maxBody = 64 << 10
	maxText = 16 << 10
	maxMeta = 2000
)

// DefaultBrief is the router's first prompt when no brief_template is set.
//
//go:embed brief.tmpl
var DefaultBrief string

// BriefData is what a brief template is rendered with.
type BriefData struct {
	// Source is the source the message was posted as, such as iphone.
	Source string
	// Client is the name of the client whose token posted it.
	Client string
	// Origin is the source's note from [inbox.origins].
	Origin string
	// Message is the text, trimmed.
	Message string
	// Meta is the post's meta object as JSON, cut at 2000 bytes; empty
	// when it sent none.
	Meta string
	// Group is the group the router is filed in; empty is the root.
	Group string
	// Received is when the message arrived, as 2006-01-02 15:04 MST.
	Received string
}

// Spawn is one router session to start.
type Spawn struct {
	Name   string
	Prompt string
}

// Spawner starts a router session and returns what to report as its result.
type Spawner interface {
	Spawn(ctx context.Context, spawn Spawn) (any, error)
}

// Server is the /inbox and /healthz handler.
type Server struct {
	cfg     Config
	tokens  map[string]identity
	brief   *template.Template
	spawner Spawner
	limit   *limiter
	log     *slog.Logger
	now     func() time.Time
}

// New builds the handler. brief is the template's text; empty is
// DefaultBrief.
func New(cfg Config, tokens map[string]identity, brief string, spawner Spawner, log *slog.Logger) (*Server, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no inbox tokens: set %s<CLIENT> or write <client>=<token> lines to the tokens file", TokenEnvPrefix)
	}
	if brief == "" {
		brief = DefaultBrief
	}
	parsed, err := template.New("brief").Option("missingkey=error").Parse(brief)
	if err != nil {
		return nil, fmt.Errorf("brief template: %w", err)
	}
	server := &Server{
		cfg:     cfg,
		tokens:  tokens,
		brief:   parsed,
		spawner: spawner,
		log:     log,
		now:     time.Now,
	}
	server.limit = &limiter{max: cfg.spawnLimit(), window: cfg.spawnWindow(), now: func() time.Time { return server.now() }}
	// A template that cannot render fails here, not on the first message.
	if _, err := server.render(BriefData{Source: "test", Client: "test", Message: "test"}); err != nil {
		return nil, fmt.Errorf("brief template: %w", err)
	}
	return server, nil
}

// ReadBrief reads the configured brief template; empty when none is set.
func ReadBrief(cfg Config) (string, error) {
	if cfg.BriefTemplate == "" {
		return "", nil
	}
	path := cfg.BriefTemplate
	if home, err := os.UserHomeDir(); err == nil {
		path = expandHome(path, home)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("brief template: %w", err)
	}
	return string(data), nil
}

// Serve listens on the configured address until ctx is done.
func (s *Server) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.listen())
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(s.cfg.listen())
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		s.log.Warn("inbox listening beyond loopback; expose it only over a private network", "listen", s.cfg.listen())
	}
	names := make([]string, 0, len(s.tokens))
	for _, id := range s.tokens {
		names = append(names, id.name)
	}
	s.log.Info("inbox serving", "listen", listener.Addr().String(), "clients", strings.Join(names, ","))
	server := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz" && r.Method == http.MethodGet:
		reply(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/inbox" && r.Method == http.MethodPost:
		s.post(w, r)
	case r.URL.Path == "/inbox" || r.URL.Path == "/healthz":
		reply(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
	default:
		reply(w, http.StatusNotFound, errorBody("not found"))
	}
}

type postBody struct {
	Text       *string         `json:"text"`
	Transcript *string         `json:"transcript"`
	Source     *string         `json:"source"`
	Meta       json.RawMessage `json:"meta"`
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	status, client, source := http.StatusOK, "", ""
	defer func() {
		// The message text is never logged.
		s.log.Info("inbox post", "remote", r.RemoteAddr, "client", client, "source", source, "status", status)
	}()
	fail := func(code int, message string) {
		status = code
		reply(w, code, errorBody(message))
	}

	id, ok := s.authenticate(r.Header.Get("Authorization"))
	if !ok {
		fail(http.StatusUnauthorized, "bad or missing bearer token")
		return
	}
	client = id.name

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		fail(http.StatusRequestEntityTooLarge, "body must be 1 byte to 64 KiB of JSON")
		return
	case err != nil:
		fail(http.StatusBadRequest, "could not read the body")
		return
	case len(raw) == 0:
		fail(http.StatusBadRequest, "body must be 1 byte to 64 KiB of JSON")
		return
	}
	var body postBody
	if err := json.Unmarshal(raw, &body); err != nil {
		fail(http.StatusBadRequest, "body must be a JSON object with string text, source and an object meta")
		return
	}

	// transcript is accepted for clients that send speech-to-text under
	// that name; text wins when both are set.
	text := ""
	if body.Text != nil && *body.Text != "" {
		text = *body.Text
	} else if body.Transcript != nil {
		text = *body.Transcript
	}
	source = id.defaultSource()
	if body.Source != nil && *body.Source != "" {
		source = strings.ToLower(*body.Source)
	}
	if strings.TrimSpace(text) == "" {
		fail(http.StatusBadRequest, "text is required")
		return
	}
	if len(text) > maxText {
		fail(http.StatusRequestEntityTooLarge, "text is over 16 KiB")
		return
	}
	if !sourcePattern.MatchString(source) {
		fail(http.StatusBadRequest, "source must be lowercase letters, digits and dashes")
		return
	}
	if !id.allows(source) {
		fail(http.StatusForbidden, fmt.Sprintf("token %q may not post as source %q", id.name, source))
		return
	}
	meta, err := metaJSON(body.Meta)
	if err != nil {
		fail(http.StatusBadRequest, "meta must be an object")
		return
	}

	release, ok := s.limit.take()
	if !ok {
		fail(http.StatusTooManyRequests, fmt.Sprintf("over %d sessions in %s, try later", s.limit.max, s.limit.window))
		return
	}
	text = strings.TrimSpace(text)
	prompt, err := s.render(BriefData{
		Source:   source,
		Client:   id.name,
		Origin:   s.cfg.origin(source),
		Message:  text,
		Meta:     meta,
		Group:    s.cfg.Group,
		Received: s.now().Format("2006-01-02 15:04 MST"),
	})
	if err != nil {
		release()
		s.log.Error("inbox brief failed", "client", client, "source", source, "err", err)
		fail(http.StatusInternalServerError, "brief template failed")
		return
	}
	name := SessionName(source, text)
	result, err := s.spawner.Spawn(r.Context(), Spawn{Name: name, Prompt: prompt})
	if err != nil {
		release()
		s.log.Error("inbox spawn failed", "client", client, "source", source, "err", err)
		status = http.StatusBadGateway
		reply(w, status, map[string]any{"error": "spawn failed", "detail": err.Error()})
		return
	}
	s.log.Info("inbox spawned", "session", name, "client", client, "source", source)
	reply(w, http.StatusOK, map[string]any{"ok": true, "session": name, "message": "Sent to " + name, "result": result})
}

func (s *Server) authenticate(header string) (identity, bool) {
	scheme, token, _ := strings.Cut(header, " ")
	token = strings.TrimSpace(token)
	if !strings.EqualFold(scheme, "bearer") || token == "" {
		return identity{}, false
	}
	// Every token is compared, so the time taken does not say which one
	// came close.
	var found identity
	ok := false
	for candidate, id := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			found, ok = id, true
		}
	}
	return found, ok
}

func (s *Server) render(data BriefData) (string, error) {
	var out strings.Builder
	if err := s.brief.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func metaJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", err
	}
	if len(meta) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxMeta {
		cut := maxMeta
		for cut > 0 && !utf8.RuneStart(encoded[cut]) {
			cut--
		}
		encoded = encoded[:cut]
	}
	return string(encoded), nil
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// SessionName is inbox-<source>-<the message's first words>.
func SessionName(source, text string) string {
	slug := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(text), "-"), "-")
	words := strings.Split(slug, "-")
	if len(words) > 5 {
		words = words[:5]
	}
	slug = strings.Join(words, "-")
	if len(slug) > 36 {
		slug = slug[:36]
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "message"
	}
	return "inbox-" + source + "-" + slug
}

func errorBody(message string) map[string]any {
	return map[string]any{"error": message}
}

func reply(w http.ResponseWriter, code int, body any) {
	data, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

// limiter allows at most max spawns in any window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	recent []time.Time
}

// take reserves a spawn; release hands it back for one that failed, which
// does not count.
func (l *limiter) take() (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.recent[:0]
	for _, at := range l.recent {
		if now.Sub(at) < l.window {
			kept = append(kept, at)
		}
	}
	l.recent = kept
	if len(l.recent) >= l.max {
		return nil, false
	}
	l.recent = append(l.recent, now)
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			for i := len(l.recent) - 1; i >= 0; i-- {
				if l.recent[i].Equal(now) {
					l.recent = append(l.recent[:i], l.recent[i+1:]...)
					return
				}
			}
		})
	}, true
}

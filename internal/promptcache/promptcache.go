// Package promptcache prices an injected message before it is sent.
//
// Typing into a live agent session costs nothing while its prompt cache is
// still warm and the price of the whole context once that cache has expired.
// Claude Code writes both numbers into its own transcript on every assistant
// record, so the answer is read rather than guessed: the cache_creation block
// says which time-to-live the turn bought, and cache_read_input_tokens says
// how large the context that would have to be rebuilt is.
package promptcache

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// The two lifetimes Anthropic's cache offers. A turn that bought the long
// one records ephemeral_1h_input_tokens; every other turn is on the short one.
const (
	TTLShort = 5 * time.Minute
	TTLLong  = time.Hour
)

// State is what the transcript says about one session's cache.
type State struct {
	// Known is false when no transcript could be read, which is not the same
	// as a cold one: it means the price is unknown, and an unknown price is
	// treated as the expensive one.
	Known bool
	// LastTurnAt is the timestamp on the newest assistant record, which is
	// when the cache was last written and so when its clock started.
	LastTurnAt time.Time
	TTL        time.Duration
	// ContextTokens is what a cold send would re-create.
	ContextTokens int
	Transcript    string
	Model         string
	Usage         *Usage
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheCreation            struct {
		Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

// Margin keeps a session that is about to expire out of the warm set. A
// sweep reads its plan, waits for a person to approve it, and then sends
// through a stagger, so "warm right now" is not the question being asked --
// "still warm when the message lands" is.
func Margin(ttl time.Duration) time.Duration {
	margin := ttl / 5
	if margin < 30*time.Second {
		margin = 30 * time.Second
	}
	return margin
}

// Warm reports whether the cache will still be there when a message sent now
// arrives, which is the only sense in which warmth matters.
func (s State) Warm(now time.Time, margin time.Duration) bool {
	if !s.Known || s.TTL <= 0 || s.LastTurnAt.IsZero() {
		return false
	}
	return now.Sub(s.LastTurnAt) < s.TTL-margin
}

func (s State) Age(now time.Time) time.Duration {
	if s.LastTurnAt.IsZero() {
		return 0
	}
	return now.Sub(s.LastTurnAt)
}

// Reader resolves a working directory to the Claude Code transcript for the
// conversation running in it.
type Reader struct {
	root string
}

func NewReader(root string) *Reader { return &Reader{root: root} }

// DefaultRoot is where Claude Code keeps its transcripts.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

var plainToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mangle is Claude Code's own project-directory naming: every character that
// is not a letter or a digit becomes a hyphen.
var mangle = regexp.MustCompile(`[^a-zA-Z0-9]`)

func (r *Reader) ProjectDir(cwd string) string {
	if r == nil || r.root == "" || cwd == "" {
		return ""
	}
	return filepath.Join(r.root, mangle.ReplaceAllString(cwd, "-"))
}

// Transcript names the file holding a session's conversation. A session
// launched with a chosen id owns the file named after it. A pane the manager
// only adopted has no id, so the newest transcript in its directory is the
// best available answer -- good enough alone, wrong when two sessions share
// a directory, which is why Plan refuses a path two sessions resolve to.
func (r *Reader) Transcript(cwd, agentSessionID string) string {
	dir := r.ProjectDir(cwd)
	if dir == "" {
		return ""
	}
	if agentSessionID != "" {
		// The id is read back out of a store row and pasted into a path, so
		// anything but a plain token resolves to nothing rather than to some
		// other directory's transcript.
		if !plainToken.MatchString(agentSessionID) {
			return ""
		}
		path := filepath.Join(dir, agentSessionID+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var found []candidate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		found = append(found, candidate{filepath.Join(dir, entry.Name()), info.ModTime()})
	}
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	return found[0].path
}

// Lookup reads a session's cache state. An unreadable or unrecognised
// transcript comes back not-Known, which every caller treats as cold.
func (r *Reader) Lookup(cwd, agentSessionID string) State {
	path := r.Transcript(cwd, agentSessionID)
	if path == "" {
		return State{}
	}
	return ReadTranscript(path)
}

// tailChunk is the first slice of a transcript read, and tailCap the most
// that is ever read. A transcript grows past 30MB and only its newest
// assistant record matters, so the file is walked backwards from the end in
// doubling chunks: nearly every session answers inside the first one, and a
// session whose last turn returned a very large tool result still resolves
// without the whole file being paged in.
const (
	tailChunk = 256 << 10
	tailCap   = 16 << 20
)

// ReadTranscript reads the newest assistant record carrying usage.
func ReadTranscript(path string) State {
	file, err := os.Open(path)
	if err != nil {
		return State{}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return State{}
	}
	size := info.Size()
	for window := int64(tailChunk); ; window *= 2 {
		if window > size {
			window = size
		}
		buf := make([]byte, window)
		if _, err := file.ReadAt(buf, size-window); err != nil && err != io.EOF {
			return State{}
		}
		if state, ok := lastUsage(buf, window < size); ok {
			state.Transcript = path
			return state
		}
		if window >= size || window >= tailCap {
			return State{}
		}
	}
}

// usageRecord is the shape of the fields this package reads. Everything else
// in a transcript record is ignored.
type usageRecord struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	IsAPIError bool      `json:"isApiErrorMessage"`
	Message    struct {
		Model string `json:"model"`
		Usage *Usage `json:"usage"`
	} `json:"message"`
}

// partial drops the first line, which a mid-file read cuts in half.
func lastUsage(buf []byte, partial bool) (State, bool) {
	lines := bytes.Split(buf, []byte("\n"))
	if partial && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec usageRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Timestamp.IsZero() || rec.IsAPIError || rec.Message.Model == "<synthetic>" {
			continue
		}
		usage := rec.Message.Usage
		if usage == nil {
			continue
		}
		if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.CacheCreationInputTokens < 0 {
			return State{}, true
		}
		cached := usage.CacheReadInputTokens + usage.CacheCreationInputTokens
		ttl := TTLShort
		if usage.CacheCreation.Ephemeral1h > 0 {
			ttl = TTLLong
		}
		if cached == 0 {
			ttl = 0
		}
		return State{
			Known:         cached > 0,
			LastTurnAt:    rec.Timestamp,
			TTL:           ttl,
			ContextTokens: cached,
			Model:         rec.Message.Model,
			Usage:         usage,
		}, true
	}
	return State{}, false
}

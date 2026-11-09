package tracing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// A span leaves the machine, and a trace dataset is shared. So what a span
// says about where it ran is filtered here, on the exporter's goroutine,
// rather than trusted to every call site: a session id, an account, a tmux
// socket go out as keyed ids, a directory not at all, an error's text with
// its URLs and addresses blanked, and every path anywhere hashed segment by
// segment (paths.go).
//
// A keyed id is the first 16 hex characters of HMAC-SHA256 under this
// install's key, which is kept beside the config with mode 0600 and never
// sent. The same value always gets the same id on one install, so spans
// still join, and without the key nobody holding the traces can compute one
// from a guess. Extensions use the same key through extension.KeyedID, so
// their own ids join the board's.

// keyFile is the install's key, in the board's config directory.
const keyFile = "trace.key"

// hashed are the attributes whose value names something on this machine, and
// the namespace each is keyed under. "created" is the session a command made;
// "tool.id" is the tool an agent called, since which tools an install uses is
// its own business.
var hashed = map[string]string{
	"session":        "session",
	"created":        "session",
	"account.name":   "account",
	"account.lender": "account",
	"socket":         "tmux_socket",
	"tool.id":        "tool",
}

// dropped are attributes that cannot be made anonymous and are not needed:
// a directory is a path, which names a person and a repository.
var dropped = map[string]bool{"dir": true, "host.name": true}

// socketPrefix is an attribute family keyed by socket name.
const socketPrefix = "capture.failed."

// privatize is attrs as they may leave the machine: keyed, dropped, and with
// every path in a value hashed (paths.go). A key holding a path is dropped
// with its value: there is no telling what it was meant to say.
func privatize(attrs []Attr) []Attr {
	out := make([]Attr, 0, len(attrs))
	for _, a := range attrs {
		if hashPathsIn(a.Key) != a.Key {
			continue
		}
		switch {
		case dropped[a.Key]:
			continue
		case hashed[a.Key] != "":
			if s, ok := a.Value.(string); ok {
				a.Value = KeyedID(hashed[a.Key], s)
			}
		case strings.HasPrefix(a.Key, socketPrefix):
			a.Key = socketPrefix + KeyedID("tmux_socket", strings.TrimPrefix(a.Key, socketPrefix))
		}
		a.Value = privateValue(a.Value)
		out = append(out, a)
	}
	return out
}

var (
	urlPattern   = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// scrubMessage is an error's text with what would name a machine, a person
// or a place blanked; what failed and why stays readable.
func scrubMessage(msg string) string {
	msg = hashPathsIn(msg)
	msg = urlPattern.ReplaceAllString(msg, "<url>")
	msg = emailPattern.ReplaceAllString(msg, "<email>")
	if host := machineName(); host != "" {
		msg = strings.ReplaceAll(msg, host, "<host>")
	}
	return msg
}

// machineName is read once; it does not change under a running board.
var machineName = sync.OnceValue(func() string {
	name, _ := os.Hostname()
	return name
})

// KeyedID is value's id under this install's key in a namespace, so a session
// and an account of the same text do not share one. Empty stays empty.
func KeyedID(namespace, value string) string {
	if value == "" {
		return ""
	}
	m := hmac.New(sha256.New, installKey())
	m.Write([]byte(namespace))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// installKey reads the key, minting and saving it on first use. If it cannot
// be saved the key lasts the process: ids then join within one run, and
// still reverse to nothing.
var installKey = sync.OnceValue(func() []byte {
	if dir := keyDir(); dir != "" {
		if key, err := loadKey(dir); err == nil {
			return key
		}
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
})

// keyDir is the board's config directory, worked out as internal/config does;
// that package cannot be imported from here without a cycle.
func keyDir() string {
	if dir := strings.TrimSpace(os.Getenv("GATE_INBOX_HOME")); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "gate-inbox")
}

func loadKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, keyFile)
	if raw, err := os.ReadFile(path); err == nil {
		if key, err := hex.DecodeString(strings.TrimSpace(string(raw))); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Written whole and renamed into place, so two processes starting at
	// once settle on one key, and the loser reads the winner's.
	tmp, err := os.CreateTemp(dir, keyFile+".*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if raw, rerr := os.ReadFile(path); rerr == nil {
			if existing, derr := hex.DecodeString(strings.TrimSpace(string(raw))); derr == nil && len(existing) == 32 {
				return existing, nil
			}
		}
		return nil, err
	}
	return key, nil
}

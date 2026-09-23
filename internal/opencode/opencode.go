// Package opencode is the one place that knows which OpenCode is on this
// machine. Gate Inbox supports OpenCode v2 only: it reads the v2 tables
// (session_v2, session_message) and nothing else. OpenCode v1 shares the
// `opencode` binary name and the default database path, so the binary's
// version is still read, but only to tell an operator on v1 that it is not
// supported.
package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Schema is which OpenCode database layout a store speaks.
type Schema int

const (
	// SchemaNone is no store at all, or one without the v2 layout in it.
	SchemaNone Schema = iota
	// SchemaV2 is the OpenCode v2 layout: session_v2, session_message.
	SchemaV2
)

func (s Schema) String() string {
	switch s {
	case SchemaV2:
		return "v2"
	default:
		return "none"
	}
}

// DBPathEnv overrides the whole resolved path, which is how a scratch copy
// is pointed at during migration testing. It mirrors the GATE_OPENCODE_DB
// override the retired Go inbox honoured.
const DBPathEnv = "GATE_OPENCODE_DB"

// DBNameEnv overrides just the file name, mirroring OpenCode's own
// OPENCODE_DB override that `opencode debug paths db` respects.
const DBNameEnv = "OPENCODE_DB"

// DBPath is where OpenCode keeps its database: GATE_OPENCODE_DB wins when
// set, else OPENCODE_DB names the file under XDG_DATA_HOME (or
// ~/.local/share), exactly as `opencode debug paths db` resolves it. An
// absolute OPENCODE_DB is used as-is.
func DBPath() string {
	if override := strings.TrimSpace(os.Getenv(DBPathEnv)); override != "" {
		return override
	}
	name := strings.TrimSpace(os.Getenv(DBNameEnv))
	if name == "" {
		name = "opencode.db"
	}
	if name == ":memory:" || filepath.IsAbs(name) {
		return name
	}
	data := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "opencode", name)
}

// Probe reports which layout the store at path speaks, read-only. Anything
// unreadable is SchemaNone rather than an error: a missing store just
// contributes nothing.
func Probe(path string) Schema {
	if path == "" || path == ":memory:" {
		return SchemaNone
	}
	if _, err := os.Stat(path); err != nil {
		return SchemaNone
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return SchemaNone
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema, _ := Layout(db)
	return schema
}

// Layout reports whether an open store speaks the v2 layout. A migrated v1
// database carries it alongside the v1 tables, which are never read. A store
// that answers without it is SchemaNone; a store that cannot be read at all
// is an error, so a broken database is still reported rather than read as
// empty.
func Layout(db *sql.DB) (Schema, error) {
	var v2 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_v2'`).Scan(&v2); err != nil {
		return SchemaNone, err
	}
	if v2 > 0 {
		return SchemaV2, nil
	}
	return SchemaNone, nil
}

var versionNumber = regexp.MustCompile(`v?(\d+)\.\d+\.\d+`)

// parseMajor reads the major version out of `opencode --version` output,
// which is "1.18.30" on v1 and "opencode v2.0.1" on v2.
func parseMajor(out string) (int, bool) {
	m := versionNumber.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return major, true
}

// BinaryMajor runs `opencode --version` and reports its major version. It
// never starts a server -- --version prints and exits -- and it never fails
// startup: a missing binary just reports ok=false. bin names the binary and
// exists so tests can point it at a stub; production passes "opencode".
func BinaryMajor(bin string) (major int, full string, ok bool) {
	if bin == "" {
		bin = "opencode"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return 0, "", false
	}
	full = strings.TrimSpace(string(out))
	major, ok = parseMajor(full)
	if !ok {
		return 0, full, false
	}
	return major, full, true
}

// Report is everything the board knows about the local OpenCode on startup.
type Report struct {
	Path   string
	Schema Schema
	// Major is the `opencode` binary's major version, 0 when the binary is
	// missing or its version unreadable.
	Major   int
	Version string
}

// Detect reads the binary version and probes the store. Both halves are
// best-effort: an operator with no OpenCode gets a zero Report, not an
// error.
func Detect() Report {
	path := DBPath()
	major, full, _ := BinaryMajor("")
	return Report{Path: path, Schema: Probe(path), Major: major, Version: full}
}

var (
	cachedOnce   sync.Once
	cachedReport Report
)

// Cached is Detect memoized for the process: the binary cannot change
// under a running board in any way worth re-execing for.
func Cached() Report {
	cachedOnce.Do(func() { cachedReport = Detect() })
	return cachedReport
}

// UpgradeNotice is the one-line warning shown when the operator's opencode
// is v1, which Gate Inbox does not support, or "" when there is nothing to
// say.
func (r Report) UpgradeNotice() string {
	if r.Major == 1 {
		return "opencode v1 is not supported — upgrade to v2: curl -fsSL https://opencode.ai/v2/install | bash (config carries over)"
	}
	return ""
}

// runAPI runs `opencode api` from cwd and returns its stdout. A variable so
// tests stand in for the CLI. The subcommand talks to the background service
// (starting it when none runs), which reads the same store every session
// writes, standalone ones included.
var runAPI = func(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencode", append([]string{"api"}, args...)...)
	cmd.Dir = cwd
	return cmd.Output()
}

// Fork copies a conversation into a new session through opencode's own API
// and returns the copy's id. v2's TUI dropped the --fork flag (only `run`
// kept it), and its session.fork operation wants a message boundary rather
// than "everything": the copy runs through the newest message, so the fork
// carries the whole conversation. Run from the session's own directory.
func Fork(cwd, sessionID string) (string, error) {
	out, err := runAPI(cwd, "GET", "/api/session/"+sessionID+"/message")
	if err != nil {
		return "", fmt.Errorf("opencode: list messages of %s: %w", sessionID, err)
	}
	var messages struct {
		Data []struct {
			ID   string `json:"id"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &messages); err != nil {
		return "", fmt.Errorf("opencode: messages of %s: %w", sessionID, err)
	}
	if len(messages.Data) == 0 {
		return "", fmt.Errorf("opencode: %s has no messages to fork", sessionID)
	}
	// The listing comes newest-first today; picked by time rather than
	// position so a reordered listing still forks the whole conversation.
	newest := messages.Data[0]
	for _, message := range messages.Data[1:] {
		if message.Time.Created > newest.Time.Created {
			newest = message
		}
	}
	body, _ := json.Marshal(map[string]any{
		"boundary": map[string]string{"type": "through", "messageID": newest.ID},
	})
	out, err = runAPI(cwd, "POST", "/api/session/"+sessionID+"/fork", "-d", string(body))
	if err != nil {
		return "", fmt.Errorf("opencode: fork %s: %w", sessionID, err)
	}
	var forked struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &forked); err != nil || forked.Data.ID == "" {
		return "", fmt.Errorf("opencode: fork of %s returned no session: %s", sessionID, strings.TrimSpace(string(out)))
	}
	return forked.Data.ID, nil
}

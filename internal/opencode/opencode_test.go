package opencode

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseMajor(t *testing.T) {
	for _, tc := range []struct {
		out   string
		major int
		ok    bool
	}{
		{"1.18.30", 1, true},
		{"opencode v2.0.1", 2, true},
		{"v2.0.1", 2, true},
		{"opencode 1.18.29\n", 1, true},
		{"", 0, false},
		{"dev", 0, false},
		{"no version here", 0, false},
	} {
		major, ok := parseMajor(tc.out)
		if major != tc.major || ok != tc.ok {
			t.Errorf("parseMajor(%q) = %d,%v, want %d,%v", tc.out, major, ok, tc.major, tc.ok)
		}
	}
}

func writeDB(t *testing.T, path, schema string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
}

const v1Schema = `CREATE TABLE session (id TEXT, directory TEXT, title TEXT, parent_id TEXT, time_updated INTEGER, time_archived INTEGER);
	CREATE TABLE message (id TEXT);
	CREATE TABLE part (id TEXT, message_id TEXT, session_id TEXT, data TEXT, time_created INTEGER, time_updated INTEGER);`

const v2Schema = `CREATE TABLE session_v2 (id TEXT, directory TEXT, title TEXT, parent_id TEXT, time_updated INTEGER, time_archived INTEGER);
	CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT);`

func TestProbe(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1.db")
	v2 := filepath.Join(dir, "v2.db")
	both := filepath.Join(dir, "both.db")
	empty := filepath.Join(dir, "empty.db")
	writeDB(t, v1, v1Schema)
	writeDB(t, v2, v2Schema)
	writeDB(t, both, v1Schema+";"+v2Schema)
	writeDB(t, empty, `CREATE TABLE kv (key TEXT);`)
	for path, want := range map[string]Schema{
		v1:                            SchemaNone,
		v2:                            SchemaV2,
		both:                          SchemaV2,
		empty:                         SchemaNone,
		filepath.Join(dir, "missing"): SchemaNone,
		"":                            SchemaNone,
		":memory:":                    SchemaNone,
	} {
		if got := Probe(path); got != want {
			t.Errorf("Probe(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDBPath(t *testing.T) {
	t.Setenv(DBPathEnv, "")
	t.Setenv(DBNameEnv, "")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DBPath(), "/tmp/xdg/opencode/opencode.db"; got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
	t.Setenv(DBNameEnv, "v2test.db")
	if got, want := DBPath(), "/tmp/xdg/opencode/v2test.db"; got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
	t.Setenv(DBNameEnv, "/abs/custom.db")
	if got := DBPath(); got != "/abs/custom.db" {
		t.Errorf("DBPath() = %q, want absolute passthrough", got)
	}
	t.Setenv(DBPathEnv, "/scratch/copy.db")
	if got := DBPath(); got != "/scratch/copy.db" {
		t.Errorf("DBPath() = %q, want full override", got)
	}
}

func TestBinaryMajorStub(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'opencode v2.0.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	major, full, ok := BinaryMajor(stub)
	if !ok || major != 2 || full != "opencode v2.0.1" {
		t.Errorf("BinaryMajor(stub) = %d,%q,%v", major, full, ok)
	}
	if _, _, ok := BinaryMajor(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("BinaryMajor(missing) ok, want false")
	}
}

func TestUpgradeNotice(t *testing.T) {
	if got := (Report{Major: 1}).UpgradeNotice(); !strings.Contains(got, "not supported") {
		t.Errorf("v1 binary: want the unsupported warning, got %q", got)
	}
	for _, r := range []Report{{Major: 2}, {Major: 2, Schema: SchemaV2}, {}} {
		if got := r.UpgradeNotice(); got != "" {
			t.Errorf("%+v: want silence, got %q", r, got)
		}
	}
}

// A fork copies through the newest message, whatever order the listing
// comes in, and returns the id the API minted.
func TestForkCopiesThroughTheNewestMessage(t *testing.T) {
	saved := runAPI
	t.Cleanup(func() { runAPI = saved })
	var calls [][]string
	runAPI = func(cwd string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{cwd}, args...))
		switch args[0] {
		case "GET":
			return []byte(`{"data":[{"id":"msg_old","time":{"created":10}},{"id":"msg_new","time":{"created":30}},{"id":"msg_mid","time":{"created":20}}]}`), nil
		case "POST":
			if !strings.Contains(args[3], `"messageID":"msg_new"`) || !strings.Contains(args[3], `"type":"through"`) {
				t.Fatalf("fork body = %s", args[3])
			}
			return []byte(`{"data":{"id":"ses_fork","fork":{"sessionID":"ses_src"}}}`), nil
		}
		return nil, errors.New("unexpected " + args[0])
	}
	id, err := Fork("/repo", "ses_src")
	if err != nil || id != "ses_fork" {
		t.Fatalf("Fork = %q, %v", id, err)
	}
	if len(calls) != 2 || calls[0][0] != "/repo" || calls[0][2] != "/api/session/ses_src/message" || calls[1][2] != "/api/session/ses_src/fork" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestForkRefusesAnEmptyConversation(t *testing.T) {
	saved := runAPI
	t.Cleanup(func() { runAPI = saved })
	runAPI = func(string, ...string) ([]byte, error) { return []byte(`{"data":[]}`), nil }
	if _, err := Fork("/repo", "ses_src"); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("err = %v", err)
	}
}

// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package agentsession

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func codexRollout(sessionID, cwd string, created time.Time) string {
	return `{"timestamp":"` + created.Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"session_id":"` +
		sessionID + `","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-07-18T14:36:09Z","type":"event_msg","payload":{}}` + "\n"
}

func TestCaptureCodexPicksSessionAfterLaunchInCwd(t *testing.T) {
	root := t.TempDir()
	launch := time.Now()
	// An older conversation in the same cwd predates the launch: not ours.
	writeFile(t, filepath.Join(root, "2026/07/18/rollout-old.jsonl"),
		codexRollout("old-uuid", "/repo", launch.Add(-time.Hour)), launch.Add(-time.Hour))
	// A conversation in a different cwd started after launch: not ours.
	writeFile(t, filepath.Join(root, "2026/07/18/rollout-other.jsonl"),
		codexRollout("other-uuid", "/elsewhere", launch.Add(time.Second)), launch.Add(time.Second))
	// Ours: same cwd, written just after launch.
	writeFile(t, filepath.Join(root, "2026/07/18/rollout-ours.jsonl"),
		codexRollout("ours-uuid", "/repo", launch.Add(2*time.Second)), launch.Add(2*time.Second))

	id, ok := captureCodex(root, "/repo", launch, map[string]bool{}, Evidence{})
	if !ok || id != "ours-uuid" {
		t.Fatalf("got id=%q ok=%v, want ours-uuid true", id, ok)
	}
}

func TestCaptureCodexSkipsClaimed(t *testing.T) {
	root := t.TempDir()
	launch := time.Now()
	writeFile(t, filepath.Join(root, "a/rollout-1.jsonl"),
		codexRollout("first-uuid", "/repo", launch.Add(time.Second)), launch.Add(time.Second))
	writeFile(t, filepath.Join(root, "a/rollout-2.jsonl"),
		codexRollout("second-uuid", "/repo", launch.Add(2*time.Second)), launch.Add(2*time.Second))

	// first-uuid already belongs to another session, so the earliest
	// unclaimed match wins instead.
	id, ok := captureCodex(root, "/repo", launch, map[string]bool{"first-uuid": true}, Evidence{})
	if !ok || id != "second-uuid" {
		t.Fatalf("got id=%q ok=%v, want second-uuid true", id, ok)
	}
}

func TestCaptureCodexNoMatch(t *testing.T) {
	root := t.TempDir()
	launch := time.Now()
	writeFile(t, filepath.Join(root, "a/rollout-1.jsonl"),
		codexRollout("x", "/other", launch.Add(time.Second)), launch.Add(time.Second))
	if id, ok := captureCodex(root, "/repo", launch, map[string]bool{}, Evidence{}); ok {
		t.Fatalf("expected no match, got %q", id)
	}
}

type ocMeta struct {
	dir     string
	created time.Time
}

// stubOpencode replaces the opencode CLI seams with in-memory data for the
// duration of a test and returns a restore function.
func stubOpencode(t *testing.T, ids []string, metas map[string]ocMeta) {
	t.Helper()
	listSaved, metaSaved := opencodeListIDs, opencodeSessionMeta
	opencodeListIDs = func(string) ([]string, bool) { return ids, true }
	opencodeSessionMeta = func(_, id string) (string, time.Time, bool) {
		m, ok := metas[id]
		return m.dir, m.created, ok
	}
	t.Cleanup(func() { opencodeListIDs, opencodeSessionMeta = listSaved, metaSaved })
}

func TestCaptureOpencodePicksSessionAfterLaunchInCwd(t *testing.T) {
	launch := time.Now()
	stubOpencode(t, []string{"ses_ours", "ses_other", "ses_old"}, map[string]ocMeta{
		// An older conversation in the same cwd predates the launch: not ours.
		"ses_old": {"/repo", launch.Add(-time.Hour)},
		// A conversation in a different cwd started after launch: not ours.
		"ses_other": {"/elsewhere", launch.Add(time.Second)},
		// Ours: same cwd, created just after launch.
		"ses_ours": {"/repo", launch.Add(2 * time.Second)},
	})

	id, ok := captureOpencode("/repo", launch, map[string]bool{})
	if !ok || id != "ses_ours" {
		t.Fatalf("got id=%q ok=%v, want ses_ours true", id, ok)
	}
}

func TestCaptureOpencodeSkipsClaimed(t *testing.T) {
	launch := time.Now()
	stubOpencode(t, []string{"ses_1", "ses_2"}, map[string]ocMeta{
		"ses_1": {"/repo", launch.Add(time.Second)},
		"ses_2": {"/repo", launch.Add(2 * time.Second)},
	})

	// ses_1 already belongs to another session, so the earliest unclaimed
	// match wins instead.
	id, ok := captureOpencode("/repo", launch, map[string]bool{"ses_1": true})
	if !ok || id != "ses_2" {
		t.Fatalf("got id=%q ok=%v, want ses_2 true", id, ok)
	}
}

func TestParseOpencodeExportReadsV2LocationDirectory(t *testing.T) {
	out := []byte("Exporting session: ses_x\n" +
		`{"info":{"id":"ses_x","time":{"created":1784385368000},"title":"Do the thing","location":{"directory":"/repo"}}}`)
	dir, created, ok := parseOpencodeExport(out)
	if !ok || dir != "/repo" || created.UnixMilli() != 1784385368000 {
		t.Fatalf("got dir=%q created=%v ok=%v", dir, created, ok)
	}
}

func TestParseOpencodeExportRefusesDirectorylessInfo(t *testing.T) {
	out := []byte(`{"info":{"id":"ses_x","time":{"created":1784385368000}}}`)
	if _, _, ok := parseOpencodeExport(out); ok {
		t.Fatal("expected no match without any directory")
	}
}

// stubOpencodeHead replaces runOpencodeHead with a fake CLI surface and
// records every argv it was called with.
func stubOpencodeHead(t *testing.T, fn func(args []string) ([]byte, error)) *[][]string {
	t.Helper()
	saved := runOpencodeHead
	var calls [][]string
	runOpencodeHead = func(_ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return fn(args)
	}
	t.Cleanup(func() { runOpencodeHead = saved })
	return &calls
}

func argvEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A v2 CLI answers `session export` with the location-nested shape on the
// first call; the bare form must never be reached.
func TestOpencodeSessionMetaV2UsesSessionExport(t *testing.T) {
	v2 := []byte(`{"info":{"id":"ses_v2","time":{"created":1784385368000},"location":{"directory":"/repo"}}}`)
	calls := stubOpencodeHead(t, func(args []string) ([]byte, error) {
		if argvEqual(args, []string{"session", "export", "ses_v2"}) {
			return v2, nil
		}
		return nil, errors.New("unexpected argv")
	})
	dir, created, ok := opencodeSessionMeta("/repo", "ses_v2")
	if !ok || dir != "/repo" || created.UnixMilli() != 1784385368000 {
		t.Fatalf("got dir=%q created=%v ok=%v", dir, created, ok)
	}
	if len(*calls) != 1 {
		t.Fatalf("got %d CLI calls, want exactly the v2 form", len(*calls))
	}
}

func TestCaptureUnknownStore(t *testing.T) {
	if _, ok := Capture("weird", "/repo", time.Now(), map[string]bool{}, Evidence{}); ok {
		t.Fatal("unknown store should not match")
	}
}

// stubOpencodeJSON substitutes the `session list --format json` output
// snapshot and recapture read.
func stubOpencodeJSON(t *testing.T, entries []opencodeListEntry) {
	t.Helper()
	saved := opencodeSessionListJSON
	opencodeSessionListJSON = func(string) ([]opencodeListEntry, bool) { return entries, true }
	t.Cleanup(func() { opencodeSessionListJSON = saved })
}

// when exactly one store entry answers the relaunch: a resumed session
// replays an existing conversation rather than minting one, so a shared cwd
// can hold several touched entries and none of them may be guessed from.

func TestRecaptureCodexBindsOnlyWhatOutranTheSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	base := time.Now().Add(-time.Hour)
	// Written one second before the relaunch: what the snapshot sees, and
	// the exact shape of the stale-binding bug — it must not bind untouched.
	writeFile(t, filepath.Join(root, "sessions", "2026/07/18/rollout-pre.jsonl"),
		codexRollout("pre-uuid", "/repo", base), base)
	snapshot, ok := Snapshot("codex", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	if id, ok := Recapture("codex", "/repo", snapshot, map[string]bool{}); ok {
		t.Fatalf("a conversation that merely predates the relaunch must not bind, got %q", id)
	}
	// The picker's choice turns again: its rollout outruns the snapshot.
	writeFile(t, filepath.Join(root, "sessions", "2026/07/18/rollout-pre.jsonl"),
		codexRollout("pre-uuid", "/repo", base), base.Add(10*time.Second))
	id, ok := Recapture("codex", "/repo", snapshot, map[string]bool{})
	if !ok || id != "pre-uuid" {
		t.Fatalf("got id=%q ok=%v, want pre-uuid true", id, ok)
	}
}

func TestRecaptureCodexMintedAfterSnapshotBinds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	base := time.Now().Add(-time.Hour)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-old.jsonl"),
		codexRollout("old-uuid", "/repo", base), base)
	snapshot, ok := Snapshot("codex", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	// A restart mints a fresh conversation: unseen by the snapshot, it
	// qualifies without needing an advance.
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-new.jsonl"),
		codexRollout("new-uuid", "/repo", base.Add(time.Second)), base.Add(time.Second))
	id, ok := Recapture("codex", "/repo", snapshot, map[string]bool{})
	if !ok || id != "new-uuid" {
		t.Fatalf("got id=%q ok=%v, want new-uuid true", id, ok)
	}
}

func TestRecaptureCodexRefusesTwoThatOutranTheSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	base := time.Now().Add(-time.Hour)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-1.jsonl"),
		codexRollout("first-uuid", "/repo", base), base)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-2.jsonl"),
		codexRollout("second-uuid", "/repo", base), base)
	snapshot, ok := Snapshot("codex", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-1.jsonl"),
		codexRollout("first-uuid", "/repo", base), base.Add(10*time.Second))
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-2.jsonl"),
		codexRollout("second-uuid", "/repo", base), base.Add(20*time.Second))

	if id, ok := Recapture("codex", "/repo", snapshot, map[string]bool{}); ok {
		t.Fatalf("expected no match for two candidates, got %q", id)
	}
}

func TestRecaptureRefusesWithoutSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-1.jsonl"),
		codexRollout("some-uuid", "/repo", time.Now()), time.Now())
	// A nil snapshot means the relaunch predates snapshot capture; recapture
	// must refuse rather than guess from a bare cutoff.
	if id, ok := Recapture("codex", "/repo", nil, map[string]bool{}); ok {
		t.Fatalf("expected no match without a snapshot, got %q", id)
	}
}

func TestSnapshotCodexRecordsEachCwdConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	// Whole-second base, so the filesystem's own mtime granularity cannot
	// round the value the assertion compares against.
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-1.jsonl"),
		codexRollout("first-uuid", "/repo", base), base)
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-2.jsonl"),
		codexRollout("second-uuid", "/other", base.Add(time.Second)), base.Add(time.Second))

	snapshot, ok := Snapshot("codex", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	if len(snapshot) != 1 || snapshot["first-uuid"] != base.UnixNano() {
		t.Fatalf("got %v, want only first-uuid at %d", snapshot, base.UnixNano())
	}
}

// An empty store is a real pre-launch state, not a failure: any conversation
// that appears after it qualifies.
func TestRecaptureBindsAfterAnEmptySnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	snapshot := map[string]int64{}
	writeFile(t, filepath.Join(root, "sessions", "a/rollout-1.jsonl"),
		codexRollout("fresh-uuid", "/repo", time.Now()), time.Now())

	id, ok := Recapture("codex", "/repo", snapshot, map[string]bool{})
	if !ok || id != "fresh-uuid" {
		t.Fatalf("got id=%q ok=%v, want fresh-uuid true", id, ok)
	}
}

func TestSnapshotOpencodeRecordsTheListUpdateTimes(t *testing.T) {
	stubOpencodeJSON(t, []opencodeListEntry{
		{ID: "ses_ours", Directory: "/repo", Updated: 3000},
		{ID: "ses_other", Directory: "/elsewhere", Updated: 2000},
	})
	snapshot, ok := Snapshot("opencode", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	if len(snapshot) != 1 || snapshot["ses_ours"] != 3000*int64(time.Millisecond) {
		t.Fatalf("got %v, want only ses_ours at %d", snapshot, 3000*int64(time.Millisecond))
	}
}

func TestRecaptureOpencodeBindsOnlyWhatOutranTheSnapshot(t *testing.T) {
	stubOpencodeJSON(t, []opencodeListEntry{
		{ID: "ses_ours", Directory: "/repo", Updated: 1000},
		{ID: "ses_other", Directory: "/elsewhere", Updated: 2000},
	})
	snapshot, ok := Snapshot("opencode", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	if id, ok := Recapture("opencode", "/repo", snapshot, map[string]bool{}); ok {
		t.Fatalf("a conversation that merely predates the relaunch must not bind, got %q", id)
	}
	// Picking the conversation reopens it: info.time.updated advances.
	stubOpencodeJSON(t, []opencodeListEntry{
		{ID: "ses_ours", Directory: "/repo", Updated: 3000},
		{ID: "ses_other", Directory: "/elsewhere", Updated: 2000},
	})
	id, ok := Recapture("opencode", "/repo", snapshot, map[string]bool{})
	if !ok || id != "ses_ours" {
		t.Fatalf("got id=%q ok=%v, want ses_ours true", id, ok)
	}
}

func TestRecaptureOpencodeRefusesTwoThatOutranTheSnapshot(t *testing.T) {
	stubOpencodeJSON(t, []opencodeListEntry{
		{ID: "ses_1", Directory: "/repo", Updated: 1000},
		{ID: "ses_2", Directory: "/repo", Updated: 1000},
	})
	snapshot, ok := Snapshot("opencode", "/repo")
	if !ok {
		t.Fatal("snapshot failed")
	}
	stubOpencodeJSON(t, []opencodeListEntry{
		{ID: "ses_1", Directory: "/repo", Updated: 3000},
		{ID: "ses_2", Directory: "/repo", Updated: 4000},
	})
	if id, ok := Recapture("opencode", "/repo", snapshot, map[string]bool{}); ok {
		t.Fatalf("expected no match for two resumed conversations, got %q", id)
	}
}

// hermes's post-resume signal is its activity columns, not a file mtime.

func TestSnapshotCodexDistinguishesEmptyFromUnavailable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := Snapshot("codex", "/repo")
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("empty store snapshot = %v, %v; want non-nil empty, true", got, ok)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing"))
	got, ok = Snapshot("codex", "/repo")
	if ok || got != nil {
		t.Fatalf("unavailable store snapshot = %v, %v; want nil, false", got, ok)
	}
}

// A conversation the scan cannot open may be the one a relaunch picks, and a
// snapshot that left it out would let recapture bind it as new.
func TestCodexRefusesAnUnreadableConversation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file")
	}
	root, cwd := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", root)
	launch := time.Now()
	unreadable := filepath.Join(root, "sessions", "a", "rollout-unreadable.jsonl")
	writeFile(t, unreadable, codexRollout("unreadable", cwd, launch), launch)
	writeFile(t, filepath.Join(root, "sessions", "a", "rollout-readable.jsonl"), codexRollout("readable", cwd, launch.Add(time.Second)), launch.Add(time.Second))
	if snapshot, ok := Snapshot("codex", cwd); !ok || len(snapshot) != 2 {
		t.Fatalf("readable snapshot = %v, %v; want both conversations", snapshot, ok)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot, ok := Snapshot("codex", cwd); ok {
		t.Fatalf("snapshot = %v past an unreadable conversation; want unavailable", snapshot)
	}
	if id, ok := Capture("codex", cwd, launch, map[string]bool{}, Evidence{}); ok {
		t.Fatalf("captured %q past an unreadable conversation", id)
	}
}

// An oversized line is a record the scan cannot parse, not a store it cannot
// read, so the store still snapshots.
func TestCodexSkipsAnOversizedRecord(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", root)
	path := filepath.Join(root, "sessions", "a", "rollout-first.jsonl")
	writeFile(t, path, strings.Repeat("x", 1024*1024+1)+"\n", time.Now())
	if snapshot, ok := Snapshot("codex", cwd); !ok || len(snapshot) != 0 {
		t.Fatalf("snapshot = %v, %v; want empty, true", snapshot, ok)
	}
}

func TestCaptureCodexUsesCreationRatherThanRecentActivity(t *testing.T) {
	launch := time.Now().UTC()
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			root := t.TempDir()
			old := launch.Add(-time.Hour)
			first := launch.Add(time.Second)
			second := launch.Add(2 * time.Second)
			key := "id"
			if legacy {
				key = "session_id"
			}
			rollout := func(id string, created time.Time) string {
				return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{%q:%q,"timestamp":%q,"cwd":"/repo"}}`+"\n", launch.Format(time.RFC3339Nano), key, id, created.Format(time.RFC3339Nano))
			}
			writeFile(t, filepath.Join(root, "rollout-old.jsonl"), rollout("old", old), launch)
			writeFile(t, filepath.Join(root, "rollout-first.jsonl"), rollout("first", first), launch.Add(time.Minute))
			writeFile(t, filepath.Join(root, "rollout-second.jsonl"), rollout("second", second), launch.Add(3*time.Second))
			id, ok := captureCodex(root, "/repo", launch, nil, Evidence{})
			if !ok || id != "first" {
				t.Fatalf("new launch captured %q, %v; want first", id, ok)
			}
		})
	}
}

func TestCaptureCodexRefusesUnknownCreationTime(t *testing.T) {
	root := t.TempDir()
	launch := time.Now()
	writeFile(t, filepath.Join(root, "rollout-unknown.jsonl"), `{"type":"session_meta","payload":{"session_id":"unknown","cwd":"/repo"}}`+"\n", launch)
	if id, ok := captureCodex(root, "/repo", launch, nil, Evidence{}); ok {
		t.Fatalf("unknown creation bound %q", id)
	}
}

// codexConversation writes a rollout that opens on prompt and answers with
// reply, with source as the session_meta source field.
func codexConversation(t *testing.T, path, id, source string, created time.Time, prompt, reply string) {
	t.Helper()
	meta := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"timestamp":%q,"cwd":"/repo","source":%s}}`, created.Format(time.RFC3339Nano), id, created.Format(time.RFC3339Nano), source)
	message := func(role, kind, text string) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":%q,"content":[{"type":%q,"text":%q}]}}`, created.Format(time.RFC3339Nano), role, kind, text)
	}
	writeFile(t, path, strings.Join([]string{meta, message("user", "input_text", prompt), message("assistant", "output_text", reply)}, "\n")+"\n", created)
}

const (
	cleanupPrompt  = "/worktree-cleanup"
	priorityPrompt = "The monitor on my signal session means the priority is not being respected"
	cleanupReply   = "I audited every sibling worktree and found three that are superseded by merged pull requests, so they are safe to remove."
	priorityReply  = "The priority flag is dropped when the background monitor rewrites the session row, which is why the queue order ignores it."
)

// A fan-out started one Codex session seconds before a hand-started one in
// the same directory, and each spawned a guardian. The hand-started session
// must bind its own conversation, not the fan-out's nor a guardian's.
func TestCaptureCodexIgnoresConversationStartedBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	launch := time.Now().UTC()
	codexConversation(t, filepath.Join(root, "rollout-cleanup.jsonl"), "cleanup", `"cli"`, launch.Add(-2400*time.Millisecond), cleanupPrompt, cleanupReply)
	codexConversation(t, filepath.Join(root, "rollout-guardian.jsonl"), "guardian", `{"subagent":{"other":"guardian"}}`, launch.Add(-2100*time.Millisecond), "review", "ok")
	codexConversation(t, filepath.Join(root, "rollout-priority.jsonl"), "priority", `"cli"`, launch.Add(1800*time.Millisecond), priorityPrompt, priorityReply)
	codexConversation(t, filepath.Join(root, "rollout-guardian-2.jsonl"), "guardian-2", `{"subagent":{"thread_spawn":{"parent_thread_id":"priority"}}}`, launch.Add(2050*time.Millisecond), "review", "ok")

	for name, evidence := range map[string]Evidence{
		"time only": {},
		"prompt":    {Prompt: priorityPrompt},
		"screen":    {Screen: "› " + priorityPrompt + "\n• " + priorityReply},
	} {
		t.Run(name, func(t *testing.T) {
			id, ok := captureCodex(root, "/repo", launch, nil, evidence)
			if !ok || id != "priority" {
				t.Fatalf("captured %q, %v; want priority", id, ok)
			}
		})
	}
}

// Prompt and screen evidence outrank creation order: the session that sent
// the cleanup prompt binds the cleanup conversation even though another
// conversation opened in the directory first.
func TestCaptureCodexPrefersEvidenceOverCreationOrder(t *testing.T) {
	root := t.TempDir()
	launch := time.Now().UTC()
	codexConversation(t, filepath.Join(root, "rollout-priority.jsonl"), "priority", `"cli"`, launch.Add(500*time.Millisecond), priorityPrompt, priorityReply)
	codexConversation(t, filepath.Join(root, "rollout-cleanup.jsonl"), "cleanup", `"cli"`, launch.Add(900*time.Millisecond), cleanupPrompt, cleanupReply)

	for name, evidence := range map[string]Evidence{
		"prompt": {Prompt: "  /Worktree-Cleanup "},
		"screen": {Screen: "• " + strings.ToUpper(cleanupReply[:40]) + "\n  " + cleanupReply[40:]},
	} {
		t.Run(name, func(t *testing.T) {
			id, ok := captureCodex(root, "/repo", launch, nil, evidence)
			if !ok || id != "cleanup" {
				t.Fatalf("captured %q, %v; want cleanup", id, ok)
			}
		})
	}
}

// Evidence that matches no conversation narrows nothing, and the earliest
// conversation that started after launch still binds.
func TestCaptureCodexUnmatchedEvidenceFallsBackToTime(t *testing.T) {
	root := t.TempDir()
	launch := time.Now().UTC()
	codexConversation(t, filepath.Join(root, "rollout-early.jsonl"), "early", `"cli"`, launch.Add(-3*time.Second), cleanupPrompt, cleanupReply)
	codexConversation(t, filepath.Join(root, "rollout-own.jsonl"), "own", `"cli"`, launch.Add(time.Second), priorityPrompt, priorityReply)

	id, ok := captureCodex(root, "/repo", launch, nil, Evidence{Prompt: "something codex rewrote", Screen: "starting codex..."})
	if !ok || id != "own" {
		t.Fatalf("captured %q, %v; want own", id, ok)
	}
}

// A conversation that started before launch binds on evidence, which the
// time fallback would refuse it.
func TestCaptureCodexBindsEarlyConversationOnEvidence(t *testing.T) {
	root := t.TempDir()
	launch := time.Now().UTC()
	codexConversation(t, filepath.Join(root, "rollout-early.jsonl"), "early", `"cli"`, launch.Add(-3*time.Second), cleanupPrompt, cleanupReply)

	if id, ok := captureCodex(root, "/repo", launch, nil, Evidence{}); ok {
		t.Fatalf("time alone captured %q; want no match", id)
	}
	if id, ok := captureCodex(root, "/repo", launch, nil, Evidence{Prompt: cleanupPrompt}); !ok || id != "early" {
		t.Fatalf("prompt captured %q, %v; want early", id, ok)
	}
}

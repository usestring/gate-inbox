package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	threadA = "0190a000-0000-7000-8000-00000000000a"
	threadB = "0190a000-0000-7000-8000-00000000000b"
)

// screens is a fake pane capture: what each pane shows, by socket and pane.
type screens map[string]string

func (s screens) capture(socket, pane string) (string, error) {
	if text, ok := s[socket+pane]; ok {
		return text, nil
	}
	return "", errors.New("no such pane")
}

func codexScreen(prompt string) string {
	return "╭─────╮\n│ >_ OpenAI Codex │\n╰─────╯\n› " + prompt + "\n• Working (0s • esc to interrupt)\n› Ask Codex to do anything\n  ← for agents · ? for shortcuts\n"
}

func promptPayload(thread, prompt, cwd string) []byte {
	raw, _ := json.Marshal(map[string]string{"session_id": thread, "prompt": prompt, "cwd": cwd, "hook_event_name": "UserPromptSubmit"})
	return raw
}

func steeringText() string { return "board steering" }

func additionalContext(t *testing.T, out string) string {
	t.Helper()
	if out == "" {
		return ""
	}
	var parsed struct {
		H struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || parsed.H.Event != "UserPromptSubmit" {
		t.Fatalf("hook output %q", out)
	}
	return parsed.H.Context
}

// A thread the board does not hold, with no adopted codex to bind it to,
// gets nothing: no output, no file.
func TestDispatchCodexIgnoresThreadsOffTheBoard(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.SyncCodex([]CodexRow{{ID: "launched1", Thread: threadB}}); err != nil {
		t.Fatal(err)
	}
	before := dirEntries(t, m.Dir())
	for _, event := range CodexEvents {
		if out := m.DispatchCodex(event, promptPayload(threadA, "hello", "/w"), screens{}.capture, steeringText); out != "" {
			t.Fatalf("%s printed %q", event, out)
		}
	}
	if out := m.DispatchCodex("PreToolUse", promptPayload(threadB, "x", "/w"), nil, steeringText); out != "" {
		t.Fatalf("an unregistered event printed %q", out)
	}
	if after := dirEntries(t, m.Dir()); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("files changed: %v -> %v", before, after)
	}
}

// The first prompt of an adopted pane's thread binds it: the one adopted
// row whose screen shows the prompt gets the thread, through its
// conversation mailbox and threads/ at once, the board's steering once, and
// the turn's status. A new thread in the same pane -- a /new -- hears the
// steering again.
func TestDispatchCodexBindsAnAdoptedThreadFromItsPrompt(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.SyncCodex([]CodexRow{
		{ID: "cxa", Adopted: true, Socket: "/s", Pane: "%1", Cwd: "/w"},
		{ID: "cxb", Adopted: true, Socket: "/s", Pane: "%2", Cwd: "/elsewhere"},
	}); err != nil {
		t.Fatal(err)
	}
	panes := screens{"/s%1": codexScreen("an older prompt"), "/s%2": codexScreen("Summarise the repository layout please")}
	out := m.DispatchCodex("UserPromptSubmit", promptPayload(threadA, "Summarise the repository   layout please\nand more", "/w"), panes.capture, steeringText)
	if got := additionalContext(t, out); got != "board steering" {
		t.Fatalf("first prompt context = %q", got)
	}
	if id, adopted, ok := m.CodexThreadRow(threadA); !ok || id != "cxb" || !adopted {
		t.Fatalf("thread bound to %q adopted=%v ok=%v, want cxb", id, adopted, ok)
	}
	if conversation, _ := m.ReadConversation("cxb"); conversation != threadA {
		t.Fatalf("mailbox = %q", conversation)
	}
	// The poller's next sync, before it stored the binding, keeps it.
	if err := m.SyncCodex([]CodexRow{
		{ID: "cxa", Adopted: true, Socket: "/s", Pane: "%1", Cwd: "/w"},
		{ID: "cxb", Adopted: true, Socket: "/s", Pane: "%2", Cwd: "/elsewhere"},
	}); err != nil {
		t.Fatal(err)
	}
	if id, _, ok := m.CodexThreadRow(threadA); !ok || id != "cxb" {
		t.Fatal("a sync between the hook and the poller unbound the thread")
	}
	if out := m.DispatchCodex("UserPromptSubmit", promptPayload(threadA, "next", "/w"), panes.capture, steeringText); out != "" {
		t.Fatalf("second prompt printed %q", out)
	}
	if out := m.DispatchCodex("Stop", promptPayload(threadA, "", "/w"), panes.capture, steeringText); out != "" {
		t.Fatalf("Stop printed %q", out)
	}
	events, _, _ := m.Events("cxb", 0)
	var got []string
	for _, ev := range events {
		got = append(got, ev.State+" "+ev.Name)
	}
	if strings.Join(got, ",") != "working UserPromptSubmit,working UserPromptSubmit,finished Stop" {
		t.Fatalf("status log = %v", got)
	}
	if state, _, ok := m.CodexHookStatus("cxb"); !ok || state != "finished" {
		t.Fatalf("CodexHookStatus = %q %v", state, ok)
	}

	// /new: the pane's next thread, bound once the board stores it.
	panes["/s%2"] = codexScreen("A fresh start")
	out = m.DispatchCodex("UserPromptSubmit", promptPayload(threadB, "A fresh start", "/elsewhere"), panes.capture, steeringText)
	if additionalContext(t, out) != "board steering" {
		t.Fatalf("a new thread in the pane was not steered: %q", out)
	}
}

// Two panes showing the same prompt bind neither; a pane the board does not
// hold is never read; a launched row's thread gets status but no steering,
// since its launch already carried it.
func TestDispatchCodexNeverGuesses(t *testing.T) {
	m := NewManager(t.TempDir())
	rows := []CodexRow{
		{ID: "cxa", Adopted: true, Socket: "/s", Pane: "%1", Cwd: "/w"},
		{ID: "cxb", Adopted: true, Socket: "/s", Pane: "%2", Cwd: "/w"},
		{ID: "launched1", Thread: threadB},
	}
	if err := m.SyncCodex(rows); err != nil {
		t.Fatal(err)
	}
	panes := screens{"/s%1": codexScreen("run the tests"), "/s%2": codexScreen("run the tests"), "/s%9": codexScreen("other")}
	if out := m.DispatchCodex("UserPromptSubmit", promptPayload(threadA, "run the tests", "/w"), panes.capture, steeringText); out != "" {
		t.Fatalf("an ambiguous prompt printed %q", out)
	}
	if _, _, ok := m.CodexThreadRow(threadA); ok {
		t.Fatal("an ambiguous prompt bound a row")
	}
	if out := m.DispatchCodex("UserPromptSubmit", promptPayload(threadB, "go", "/w"), panes.capture, steeringText); out != "" {
		t.Fatalf("a launched row was steered: %q", out)
	}
	if state, _, ok := m.CodexHookStatus("launched1"); !ok || state != "working" {
		t.Fatalf("launched status = %q %v", state, ok)
	}
}

// SyncCodex publishes exactly the rows it is given, and with none clears
// the watch file the prelude stops on.
func TestSyncCodexPublishesTheBoardsRows(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if err := m.SyncCodex([]CodexRow{{ID: "cxa", Thread: threadA, Adopted: true, Socket: "/s", Pane: "%1"}, {ID: "bad id", Thread: threadB}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(CodexWatchFile(dir)); err != nil {
		t.Fatal("no watch file")
	}
	if _, _, ok := m.CodexThreadRow(threadB); ok {
		t.Fatal("an unsafe row id was published")
	}
	if err := os.MkdirAll(filepath.Join(m.codexDir(), codexSteeredDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(m.codexDir(), codexSteeredDirName, "cxa"), threadA+"\n")
	if err := m.SyncCodex(nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{CodexWatchFile(dir), filepath.Join(m.codexDir(), "threads", threadA), filepath.Join(m.codexDir(), "adopted.json"), filepath.Join(m.codexDir(), codexSteeredDirName, "cxa")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s survived an empty sync", path)
		}
	}
}

func TestScreenShowsPrompt(t *testing.T) {
	screen := codexScreen("Fix the flaky test in internal/ui")
	for needle, want := range map[string]bool{
		promptNeedle("Fix the flaky test in internal/ui and report"): false,
		promptNeedle("Fix the flaky   test"):                         true,
		promptNeedle("  \n Fix the flaky test in internal/ui"):       true,
		promptNeedle("Ask Codex"):                                    true,
		promptNeedle("something else"):                               false,
	} {
		if got := ScreenShowsPrompt(screen, needle); got != want {
			t.Errorf("ScreenShowsPrompt(%q) = %v, want %v", needle, got, want)
		}
	}
	if promptNeedle(" \n\t") != "" {
		t.Fatal("a blank prompt has a needle")
	}
}

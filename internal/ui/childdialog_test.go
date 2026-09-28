package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/notify"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const stepperAskRecord = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_4q","name":"AskUserQuestion","input":{"questions":[` +
	`{"header":"Tooling","question":"Which package manager should the workspace standardise on?","multiSelect":false,"options":[{"label":"pnpm","description":"Use pnpm for the workspace"},{"label":"npm","description":"Use npm for the workspace"},{"label":"bun","description":"Use bun for the workspace"}]},` +
	`{"header":"Checks","question":"Which checks should block a merge?","multiSelect":true,"options":[{"label":"Lint","description":"Block merges on lint failures"},{"label":"Unit tests","description":"Block merges on unit test failures"},{"label":"Licence scan","description":"Block merges on licence scan failures"}]},` +
	`{"header":"Publish","question":"Should releases publish to npm automatically?","multiSelect":false,"options":[{"label":"Yes","description":"Publish on release"},{"label":"No","description":"Publish by hand"}]},` +
	`{"header":"Tests","question":"Run the full suite before each release?","multiSelect":false,"options":[{"label":"Always","description":"Every release"},{"label":"Only on main","description":"Main only"}]}` +
	`]}}]}}`

// writeChildTranscript puts a pending AskUserQuestion where childAsked looks
// for sess's transcript.
func writeChildTranscript(t *testing.T, sess store.Session, record string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sess.AgentSessionID+".jsonl"), []byte(record+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChildWaitingOnADialogNotifiesTheParentWithItsFullContent(t *testing.T) {
	cases := []struct {
		name       string
		fixture    string
		transcript bool
		want       []string
	}{
		{
			name:       "four-question stepper at 50 columns",
			fixture:    "claude-2.1.284-w50-stepper4-first.ansi",
			transcript: true,
			want: []string{
				"a dialog asking 4 questions",
				"Question 1 of 4 [Tooling]", "Which package manager should the workspace standardise on?",
				"1. pnpm -- Use pnpm for the workspace",
				"Question 2 of 4 [Checks]", "Which checks should block a merge?", "3. Licence scan",
				"Question 3 of 4 [Publish]", "Should releases publish to npm automatically?",
				"Question 4 of 4 [Tests]", "Run the full suite before each release?", "2. Only on main",
				"one answer_session call",
				"Question 2 is a multi-select",
			},
		},
		{
			name:    "multi-select with a ticked box at 50 columns",
			fixture: "claude-2.1.284-w50-stepper4-multiselect-ticked.ansi",
			want: []string{
				"Which checks should block a merge?",
				"1. [x] Lint", "2. [ ] Unit tests", "3. [ ] Licence scan",
				"multi-select, which answer_session cannot tick",
				"Enter ticks or unticks the box",
			},
		},
		{
			name:    "multi-select at 44 columns",
			fixture: "claude-2.1.283-tabs-w44-multiselect.ansi",
			want:    []string{"Which checks must pass before merge?", "[ ] ", "multi-select"},
		},
		{
			name:    "permission prompt at 50 columns",
			fixture: "claude-2.1.284-w50-permission-bash.ansi",
			want: []string{
				"stopped on a permission prompt",
				"Bash command", "curl -s https://example.com/ -o out.html", "Do you want to proceed?",
				"1. Yes (current)", "2. Yes, and don’t ask again for: curl *", "4. No",
				"answer_session cannot answer a permission prompt", "never an agent's",
				"ask the operator for the one keystroke", "Pressing a choice's number",
			},
		},
		{
			name:    "workspace-trust dialog at 50 columns",
			fixture: "claude-2.1.284-w50-workspace-trust.ansi",
			want: []string{
				"stopped on a workspace-trust dialog", "/home/dev/work/release-toolkits",
				"1) No, exit (current)", "2) Yes, I trust this folder", "choice 1 of 2",
			},
		},
		{
			name:    "MCP-server trust dialog at 50 columns",
			fixture: "claude-2.1.284-w50-mcp-trust.ansi",
			want: []string{
				"stopped on a MCP-server trust dialog", "demo-files",
				"2) Use this and all future MCP servers in this project",
				"3) Continue without using this MCP server (current)",
			},
		},
		{
			name:    "Submit review page",
			fixture: "claude-2.1.283-tabs-w80-review-complete.ansi",
			want:    []string{"Submit answers"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, st := pollerWithStore(t)
			parent := seedSession(t, st, store.Session{ID: "parent01", Name: "release-lead", Status: status.Working})
			child := seedSession(t, st, store.Session{
				ID: "child001", Name: "release-audit", ParentID: parent.ID, Status: status.Working,
				AgentSessionID: "conv-4q", Cwd: "/work",
			})
			if tc.transcript {
				writeChildTranscript(t, child, stepperAskRecord)
			} else {
				t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			}
			if err := p.watchChildDialog(child, status.Waiting, readDialogFixture(t, tc.fixture), time.Now()); err != nil {
				t.Fatalf("watchChildDialog: %v", err)
			}
			head, found, err := st.HeadMessage(parent.ID)
			if err != nil || !found {
				t.Fatalf("the parent was not notified: found=%v err=%v", found, err)
			}
			if head.SenderID != child.ID {
				t.Errorf("message from %q, want the child", head.SenderID)
			}
			for _, want := range tc.want {
				if !strings.Contains(head.Body, want) {
					t.Errorf("notification lacks %q:\n%s", want, head.Body)
				}
			}
		})
	}
}

func TestAStandingChildDialogIsFollowedUpNotLeftSilent(t *testing.T) {
	p, st := pollerWithStore(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var pinged []notify.Note
	p.escalate = func(note notify.Note) bool { pinged = append(pinged, note); return true }
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "release-lead", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "release-audit", ParentID: parent.ID, Status: status.Working})
	pane := readDialogFixture(t, "claude-2.1.284-w50-permission-bash.ansi")
	start := time.Now()

	if err := p.watchChildDialog(child, status.Waiting, pane, start); err != nil {
		t.Fatal(err)
	}
	first, _, _ := st.HeadMessage(parent.ID)
	if err := p.watchChildDialog(child, status.Waiting, pane, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.QueuedCount(parent.ID); n != 1 {
		t.Fatalf("a standing dialog was relayed %d times inside a minute", n)
	}

	if err := p.watchChildDialog(child, status.Waiting, pane, start.Add(childDialogUndelivered)); err != nil {
		t.Fatal(err)
	}
	head, _, _ := st.HeadMessage(parent.ID)
	if head.ID == first.ID || !head.Interrupt || !strings.Contains(head.Body, "Not yet read") {
		t.Fatalf("an undelivered relay was not re-sent as an interrupt: %+v", head)
	}
	if n, _ := st.QueuedCount(parent.ID); n != 1 {
		t.Errorf("the re-send stacked behind the first instead of replacing it: %d queued", n)
	}
	if len(pinged) != 1 {
		t.Errorf("the operator was pinged %d times, want 1", len(pinged))
	}

	delivered := start.Add(childDialogUndelivered + time.Minute)
	if err := st.MarkDelivered(head.ID, delivered); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Duration{childDialogRemind, 2 * childDialogRemind, 3 * childDialogRemind, 4 * childDialogRemind} {
		now := delivered.Add(at)
		if err := p.watchChildDialog(child, status.Waiting, pane, now); err != nil {
			t.Fatal(err)
		}
		if next, found, _ := st.HeadMessage(parent.ID); found {
			if err := st.MarkDelivered(next.ID, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	reminders, err := st.Inbox(parent.ID, store.InboxFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, msg := range reminders {
		if strings.HasPrefix(msg.Body, "Reminder ") {
			count++
		}
	}
	if count != childDialogReminders {
		t.Errorf("sent %d reminders, want %d", count, childDialogReminders)
	}
	if len(pinged) != 2 {
		t.Errorf("the operator was pinged %d times after the last reminder, want 2 in all", len(pinged))
	}

	if err := p.watchChildDialog(child, status.Working, "", delivered.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, tracked := p.childDialogs[child.ID]; tracked {
		t.Error("a child that left its dialog is still being followed up")
	}
}

func TestANewDialogOnAWaitingChildIsRelayedAgain(t *testing.T) {
	p, st := pollerWithStore(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "release-lead", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "release-audit", ParentID: parent.ID, Status: status.Waiting})
	now := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, readDialogFixture(t, "claude-2.1.284-w50-workspace-trust.ansi"), now); err != nil {
		t.Fatal(err)
	}
	if err := p.watchChildDialog(child, status.Waiting, readDialogFixture(t, "claude-2.1.284-w50-mcp-trust.ansi"), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	head, _, _ := st.HeadMessage(parent.ID)
	if !strings.Contains(head.Body, "MCP-server trust dialog") {
		t.Errorf("the second dialog was not relayed:\n%s", head.Body)
	}
	if n, _ := st.QueuedCount(parent.ID); n != 1 {
		t.Errorf("%d relays queued, want the newer to replace the older", n)
	}
}

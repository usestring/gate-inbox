// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func TestSessionCommandsParseArgumentsAndPrintSentences(t *testing.T) {
	cases := []struct {
		name    string
		run     func(*bytes.Buffer, *fakeSessions, []string) error
		args    []string
		want    string
		inspect func(*testing.T, *fakeSessions)
	}{
		{
			name: "sessions lists every row",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runSessions(out, f, args, "cafe0001")
			},
			want: "- api-worker (id beef1234) running claude in api at /repo; status=working; running=true",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.callerID != "cafe0001" {
					t.Fatalf("caller = %q", f.callerID)
				}
			},
		},
		{
			name: "send takes a target and a message",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runSend(out, f, args, "cafe0001")
			},
			args: []string{"beef1234", "ship it"},
			want: "queued message 7 for session beef1234 at position 1. It has not reached the agent yet",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.targetID != "beef1234" || f.message != "ship it" {
					t.Fatalf("send got target %q message %q", f.targetID, f.message)
				}
			},
		},
		{
			name: "read prints the pane",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runRead(out, f, args, "cafe0001")
			},
			args: []string{"beef1234"},
			want: "screen text",
		},
		{
			name: "kill names what it stopped",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runKill(out, f, args, "cafe0001")
			},
			args: []string{"beef1234"},
			want: "killed api-worker (id beef1234)",
		},
		{
			name: "revive names what it brought back",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runRevive(out, f, args, "cafe0001")
			},
			args: []string{"beef1234"},
			want: "revived api-worker (id beef1234)",
		},
		{
			name: "migrate names the source and the row it made",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runMigrate(out, f, args, "cafe0001")
			},
			args: []string{"beef1234", "--tool", "codex", "--name", "api-worker-codex"},
			want: "migrated beef1234 to api-worker (id beef1234)",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.targetID != "beef1234" || f.migrate.Tool != "codex" || f.migrate.Name != "api-worker-codex" {
					t.Fatalf("migrate got target %q opts %+v", f.targetID, f.migrate)
				}
			},
		},
		{
			name: "park runs from any shell and says what it left",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runPark(out, f, args, "")
			},
			want: "parked 1 session(s); unpark will revive 1\n- api-worker (id beef1234) running claude in api at /repo\n2 of those were adopted panes, now owned rows",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.callerID != "" {
					t.Fatalf("park forwarded a caller %q it was not given", f.callerID)
				}
			},
		},
		{
			name: "park --dry-run says it only planned",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runPark(out, f, args, "")
			},
			args: []string{"--dry-run"},
			want: "dry run: would park 1 session(s); unpark will revive 1",
			inspect: func(t *testing.T, f *fakeSessions) {
				if !f.dryRun {
					t.Fatal("--dry-run did not reach the layer")
				}
			},
		},
		{
			name: "unpark warns about a revive that used --continue",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runUnpark(out, f, args, "cafe0001")
			},
			want: "revived 1 session(s); 0 still parked\n- api-worker (id beef1234)",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.callerID != "cafe0001" {
					t.Fatalf("caller = %q", f.callerID)
				}
			},
		},
		{
			name: "archive files a session away by default",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runArchive(out, f, args, "cafe0001")
			},
			args: []string{"beef1234"},
			want: "archived api-worker (id beef1234)",
			inspect: func(t *testing.T, f *fakeSessions) {
				if !f.archived {
					t.Fatal("archive without --restore should archive")
				}
			},
		},
		{
			name: "archive --restore puts it back",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runArchive(out, f, args, "cafe0001")
			},
			args: []string{"beef1234", "--restore"},
			want: "restored api-worker (id beef1234)",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.archived {
					t.Fatal("--restore should unarchive")
				}
			},
		},
		{
			name: "message-status reads the id back",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runMessageStatus(out, f, args, "cafe0001")
			},
			args: []string{"7"},
			want: "message 7 to session beef is delivered",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.messageID != 7 {
					t.Fatalf("message id = %d", f.messageID)
				}
			},
		},
		{
			name: "groups lists the tree",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runGroups(out, f, args, "cafe0001")
			},
			want: "- api; sessions=2",
		},
		{
			name: "delete-group names what it removed and moved",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runDeleteGroup(out, f, args, "cafe0001")
			},
			args: []string{"fleet"},
			want: "deleted fleet; 1 session(s) moved to the root group: beef1234",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.groupPath != "fleet" || f.callerID != "cafe0001" {
					t.Fatalf("delete-group got %q as %q", f.groupPath, f.callerID)
				}
			},
		},
		{
			name: "create-group takes a path and a directory",
			run: func(out *bytes.Buffer, f *fakeSessions, args []string) error {
				return runCreateGroup(out, f, args, "cafe0001")
			},
			args: []string{"api/web", "--directory", "/repo"},
			want: "created group api/web",
			inspect: func(t *testing.T, f *fakeSessions) {
				if f.groupPath != "api/web" || f.directory != "/repo" {
					t.Fatalf("create-group got %q %q", f.groupPath, f.directory)
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			fake := &fakeSessions{session: sampleSession()}
			if err := testCase.run(out, fake, testCase.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(out.String(), testCase.want) {
				t.Fatalf("output = %q, want it to contain %q", out.String(), testCase.want)
			}
			if testCase.inspect != nil {
				testCase.inspect(t, fake)
			}
		})
	}
}

// Only a flag the caller typed may reach the layer: a zero value passed
// anyway would silently override the group a spawn inherits.
func TestSpawnPassesOnlyTheFlagsGiven(t *testing.T) {
	out := &bytes.Buffer{}
	fake := &fakeSessions{session: sampleSession()}
	args := []string{"--name", "api-worker", "--prompt", "build the api", "--tool", "claude", "--directory", "/repo"}
	if err := runSpawn(out, fake, args, "cafe0001"); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if fake.opts.Name != "api-worker" || fake.opts.Prompt != "build the api" || fake.opts.Tool != "claude" || fake.opts.Directory != "/repo" {
		t.Fatalf("spawn opts = %+v", fake.opts)
	}
	if fake.opts.Group != nil {
		t.Fatalf("an untyped group should stay inherited, got %v", fake.opts.Group)
	}
	if !strings.HasPrefix(out.String(), "created api-worker (id beef1234)") {
		t.Fatalf("spawn output = %q", out.String())
	}

	explicit := &fakeSessions{session: sampleSession()}
	if err := runSpawn(&bytes.Buffer{}, explicit, []string{"--group", ""}, "cafe0001"); err != nil {
		t.Fatalf("spawn explicit: %v", err)
	}
	if explicit.opts.Group == nil || *explicit.opts.Group != "" {
		t.Fatalf("an explicit empty group targets the root, got %v", explicit.opts.Group)
	}
}

func TestWaitCollectsStatesAndFailsOnTimeout(t *testing.T) {
	reached := &fakeSessions{wait: sessioncmd.WaitResult{
		Session: sampleSession(), Reached: true, Outcome: sessioncmd.WaitReached, Waited: "3s", ManagerAwake: true,
	}}
	out := &bytes.Buffer{}
	if err := runWait(out, reached, []string{"beef1234", "--until", "idle,finished", "--timeout", "10s"}, "cafe0001"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(reached.until) != 2 || reached.until[0] != "idle" || reached.until[1] != "finished" {
		t.Fatalf("until = %v", reached.until)
	}
	if reached.timeout != 10*time.Second {
		t.Fatalf("timeout = %s", reached.timeout)
	}
	if !strings.Contains(out.String(), "is working after 3s") {
		t.Fatalf("wait output = %q", out.String())
	}

	timedOut := &fakeSessions{wait: sessioncmd.WaitResult{
		Session: sampleSession(), Outcome: sessioncmd.WaitTimedOut, Waited: "50s", ManagerAwake: true,
	}}
	err := runWait(&bytes.Buffer{}, timedOut, []string{"beef1234"}, "cafe0001")
	if err == nil || !strings.HasPrefix(err.Error(), "timed out:") {
		t.Fatalf("a timeout should fail the command, got %v", err)
	}
}

// The shell front has to reach the set wait too, or an agent holding only
// the subcommands is left waiting on one child at a time.
func TestWaitParksOnASetFromTheShell(t *testing.T) {
	named := &fakeSessions{wait: sessioncmd.WaitResult{
		Session: sampleSession(), Reached: true, Outcome: sessioncmd.WaitReached, Waited: "3s", ManagerAwake: true,
	}}
	if err := runWait(&bytes.Buffer{}, named, []string{"beef1234", "beef5678"}, "cafe0001"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if strings.Join(named.waitIDs, ",") != "beef1234,beef5678" || named.waitChildren {
		t.Fatalf("wait set = %v children %v", named.waitIDs, named.waitChildren)
	}

	fanOut := &fakeSessions{wait: sessioncmd.WaitResult{
		Session: sampleSession(), Reached: true, Outcome: sessioncmd.WaitReached, Waited: "3s", ManagerAwake: true,
	}}
	if err := runWait(&bytes.Buffer{}, fanOut, []string{"--children"}, "cafe0001"); err != nil {
		t.Fatalf("wait --children: %v", err)
	}
	if !fanOut.waitChildren || len(fanOut.waitIDs) != 0 {
		t.Fatalf("--children = %v ids %v", fanOut.waitChildren, fanOut.waitIDs)
	}
}

// A timeout over a set is one answer about several sessions, and the
// sentence it exits with has to carry all of them.
func TestWaitPrintsTheWholeSetsStanding(t *testing.T) {
	first := sampleSession()
	second := sampleSession()
	second.ID, second.Name, second.Status = "beef5678", "worker-two", "finished"
	result := sessioncmd.WaitResult{
		Session: second, Reached: true, Outcome: sessioncmd.WaitReached, Waited: "4s", ManagerAwake: true,
		Standing: []sessioncmd.WaitStanding{
			{Session: first, Outcome: sessioncmd.WaitTimedOut},
			{Session: second, Outcome: sessioncmd.WaitReached},
		},
	}
	out := &bytes.Buffer{}
	if err := runWait(out, &fakeSessions{wait: result}, []string{"beef1234", "beef5678"}, "cafe0001"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	printed := out.String()
	for _, want := range []string{"1 of 2 reached, 1 still working, 0 died", "[timed_out]", "[reached]", "worker-two"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("wait output %q does not carry %q", printed, want)
		}
	}
}

func TestMessageStatusRefusesANonNumericID(t *testing.T) {
	err := runMessageStatus(&bytes.Buffer{}, &fakeSessions{}, []string{"seven"}, "cafe0001")
	if err == nil || !strings.Contains(err.Error(), "gate-inbox send prints the id it queued") {
		t.Fatalf("error = %v, want it to say where the id comes from", err)
	}
}

// --as-human claims the operator's voice, so it is refused from the shell
// Claude Code runs its tool calls in, a soft boundary stated as one in the
// flag's own help.
func TestSendAsHumanIsRefusedFromAnAgentShell(t *testing.T) {
	t.Setenv(agentEnv, "1")
	fake := &fakeSessions{}
	err := runSend(&bytes.Buffer{}, fake, []string{"--as-human", "a1b2c3d4", "carry on"}, "cafe0001")
	if err == nil {
		t.Fatal("an agent shell claimed the operator's voice")
	}
	if !strings.Contains(err.Error(), agentEnv) {
		t.Fatalf("err = %v, want it to name the variable that gave it away", err)
	}
	if fake.sentHuman || fake.message != "" {
		t.Error("the refusal still queued the message")
	}
}

func TestSendChoosesTheHumanPathOnlyWithTheFlag(t *testing.T) {
	t.Setenv(agentEnv, "")
	for _, tc := range []struct {
		name  string
		args  []string
		human bool
	}{
		{"plain send", []string{"a1b2c3d4", "carry on"}, false},
		{"as human", []string{"--as-human", "a1b2c3d4", "carry on"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSessions{}
			if err := runSend(&bytes.Buffer{}, fake, tc.args, "cafe0001"); err != nil {
				t.Fatalf("runSend: %v", err)
			}
			if fake.sentHuman != tc.human {
				t.Errorf("sentHuman = %v, want %v", fake.sentHuman, tc.human)
			}
			if fake.message != "carry on" {
				t.Errorf("message = %q", fake.message)
			}
		})
	}
}

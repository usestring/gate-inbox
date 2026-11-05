// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func TestTaskVerbsDispatch(t *testing.T) {
	out := &bytes.Buffer{}
	fake := &fakeSessions{task: sessioncmd.Task{ID: "t1", Title: "wire the cli", State: "in_progress", OwnerName: "api-worker"}}

	if err := runTaskList(out, fake, nil, "cafe0001"); err != nil {
		t.Fatalf("task list: %v", err)
	}
	if !strings.Contains(out.String(), "- wire the cli (t1) [in_progress] held by api-worker") {
		t.Fatalf("task list output = %q", out.String())
	}

	created := &bytes.Buffer{}
	if err := runTaskCreate(created, fake, []string{"wire the cli", "--body", "five files", "--depends-on", "a1,b2"}, "cafe0001"); err != nil {
		t.Fatalf("task create: %v", err)
	}
	if fake.title != "wire the cli" || fake.body != "five files" {
		t.Fatalf("task create got %q / %q", fake.title, fake.body)
	}
	if len(fake.dependsOn) != 2 || fake.dependsOn[0] != "a1" || fake.dependsOn[1] != "b2" {
		t.Fatalf("depends-on = %v", fake.dependsOn)
	}
	if err := runTaskCreate(&bytes.Buffer{}, fake, []string{"handed", "--assignee", "me"}, "cafe0001"); err != nil || fake.assignee != "me" {
		t.Fatalf("task create --assignee = %v, forwarded %q", err, fake.assignee)
	}
	assigned := &bytes.Buffer{}
	if err := runTaskAssign(assigned, fake, []string{"t1", "w1"}, "cafe0001"); err != nil || fake.taskID != "t1" || fake.assignee != "w1" {
		t.Fatalf("task assign = %v, forwarded %q %q", err, fake.taskID, fake.assignee)
	}
	if !strings.HasPrefix(assigned.String(), "assigned wire the cli (t1)") {
		t.Fatalf("task assign output = %q", assigned.String())
	}
	if err := runTaskAssign(&bytes.Buffer{}, fake, []string{"t1"}, "cafe0001"); err != nil || fake.assignee != "" {
		t.Fatalf("task assign with no session = %v, forwarded %q; want an unassign", err, fake.assignee)
	}

	fake.taskID = "unset"
	if err := runTaskClaim(&bytes.Buffer{}, fake, nil, "cafe0001"); err != nil {
		t.Fatalf("task claim: %v", err)
	}
	if fake.taskID != "" || fake.claimOpts.Any {
		t.Fatalf("a bare claim takes the fleet's next task, got id %q %+v", fake.taskID, fake.claimOpts)
	}
	if err := runTaskClaim(&bytes.Buffer{}, fake, []string{"--any"}, "cafe0001"); err != nil || !fake.claimOpts.Any {
		t.Fatalf("task claim --any = %v, forwarded %+v", err, fake.claimOpts)
	}
	if err := runTaskClaim(&bytes.Buffer{}, fake, []string{"t1"}, "cafe0001"); err != nil {
		t.Fatalf("task claim t1: %v", err)
	}
	if fake.taskID != "t1" {
		t.Fatalf("claim id = %q", fake.taskID)
	}

	settled := map[string]func(io.Writer, taskCommands, []string, string) error{
		"finished": runTaskFinish,
		"released": runTaskRelease,
	}
	for verb, run := range settled {
		buf := &bytes.Buffer{}
		fake.taskID = "unset"
		if err := run(buf, fake, []string{"t1"}, "cafe0001"); err != nil {
			t.Fatalf("task %s: %v", verb, err)
		}
		if fake.taskID != "t1" {
			t.Fatalf("task %s reached the layer with id %q", verb, fake.taskID)
		}
		if !strings.HasPrefix(buf.String(), verb+" wire the cli (t1)") {
			t.Fatalf("task %s output = %q", verb, buf.String())
		}
	}

	noted := &bytes.Buffer{}
	if err := runTaskFinish(noted, fake, []string{"t1", "--note", "merged #12; docs left"}, "cafe0001"); err != nil {
		t.Fatalf("task finish --note: %v", err)
	}
	if fake.taskNote != "merged #12; docs left" {
		t.Fatalf("task finish --note reached the layer as %q", fake.taskNote)
	}

	deleted := &bytes.Buffer{}
	if err := runTaskDelete(deleted, fake, []string{"t1"}, "cafe0001"); err != nil {
		t.Fatalf("task delete: %v", err)
	}
	if !fake.deleted || deleted.String() != "deleted task t1\n" {
		t.Fatalf("task delete output = %q", deleted.String())
	}
}

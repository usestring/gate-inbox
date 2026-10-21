package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"testing"
)

func TestStopTakesOnlyTheCallerAndReportsDryRun(t *testing.T) {
	fake := &fakeSessions{}
	out := &bytes.Buffer{}
	if err := runStop(out, fake, []string{"--dry-run", "--json"}, "caller"); err != nil {
		t.Fatal(err)
	}
	if fake.callerID != "caller" || !fake.dryRun || !json.Valid(out.Bytes()) {
		t.Fatalf("stop: %+v, %s", fake, out)
	}
	if err := runStop(out, fake, []string{"another-session"}, "caller"); err == nil {
		t.Fatal("stop accepted another target")
	}
	fake.failWith = errors.New("pane identity mismatch")
	if err := runStop(out, fake, nil, "caller"); !errors.Is(err, fake.failWith) {
		t.Fatalf("lost stop failure: %v", err)
	}
}

func TestStopIfManagedOnlyAcceptsExactUnmanagedResult(t *testing.T) {
	fake := &fakeSessions{failWith: sessioncmd.ErrUnmanagedCaller}
	out := &bytes.Buffer{}
	if err := runStop(out, fake, []string{"--if-managed", "--dry-run", "--json"}, ""); err != nil {
		t.Fatal(err)
	}
	var result sessioncmd.StopResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Managed || !result.DryRun {
		t.Fatalf("unmanaged: %s, %v", out, err)
	}
	fake.failWith = errors.New("cannot read caller process")
	if err := runStop(out, fake, []string{"--if-managed", "--dry-run", "--json"}, ""); !errors.Is(err, fake.failWith) {
		t.Fatalf("verification failure became unmanaged: %v", err)
	}
}

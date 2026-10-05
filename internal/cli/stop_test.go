package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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

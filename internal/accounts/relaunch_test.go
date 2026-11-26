package accounts

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// A session coming back on its own row keeps its account while that reads,
// and its launch reads the secret only once.
func TestRelaunchKeepsAReadableAccount(t *testing.T) {
	freshMarks(t)
	ran := secrets(t, nil)
	c := &excluding{order: []string{"BOB2"}, honor: true}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return c, nil }))
	got, err := Relaunch(readable, "carol3", Request{SessionID: "s1", Reason: extension.LaunchRelaunch})
	if err != nil || got != "CAROL3" || len(c.requests) != 0 {
		t.Fatalf("Relaunch = %q %v, asked %d", got, err, len(c.requests))
	}
	reads := len(*ran)
	if token, err := Resolve(readable, got); err != nil || token != "token-CAROL3" || len(*ran) != reads {
		t.Fatalf("launch read %q %v, %d more reads", token, err, len(*ran)-reads)
	}
}

// A session whose account's secret was disabled while it was parked comes
// back on the chooser's pick: the chooser hears of the account and is asked
// with it excluded.
func TestRelaunchMovesOffAnUnavailableAccount(t *testing.T) {
	freshMarks(t)
	secrets(t, map[string]error{"CAROL3": errors.New(disabled)})
	c := &excluding{order: []string{"CAROL3", "BOB2"}, honor: true}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return c, nil }))
	got, err := Relaunch(readable, "CAROL3", Request{SessionID: "s1", ToolName: "claude", Reason: extension.LaunchRelaunch})
	if err != nil || got != "BOB2" {
		t.Fatalf("Relaunch = %q %v", got, err)
	}
	if len(c.requests) != 1 || strings.Join(c.requests[0].Exclude, ",") != "CAROL3" || c.requests[0].Reason != extension.LaunchRelaunch || c.requests[0].SessionID != "s1" {
		t.Fatalf("requests = %+v", c.requests)
	}
	if len(c.told) != 1 || c.told[0] != "s1:CAROL3:account CAROL3 is unavailable: its secret version is disabled" {
		t.Fatalf("told = %v", c.told)
	}
}

// With a chooser that has nothing else, the session comes back on the CLI's
// own login with a warning, as a launch does. With no chooser the error
// stands, as before; so does a read that failed for no reason of the
// account's.
func TestRelaunchFallsBackOrKeepsTheError(t *testing.T) {
	freshMarks(t)
	secrets(t, map[string]error{"CAROL3": errors.New(disabled), "DAVE4": errors.New("no answer within 30s")})
	c := &excluding{order: []string{"CAROL3"}, honor: true}
	restore := UseChooser(func() (extension.AccountChooser, error) { return c, nil })
	warned := ""
	got, err := Relaunch(readable, "CAROL3", Request{SessionID: "s1", Warn: func(why string) { warned = why }})
	if err != nil || got != "" || warned != "account CAROL3 is unavailable: its secret version is disabled" {
		t.Fatalf("nothing else: Relaunch = %q %v, warned %q", got, err, warned)
	}
	if got, err := Relaunch(readable, "DAVE4", Request{SessionID: "s1"}); err != nil || got != "DAVE4" {
		t.Fatalf("transient: Relaunch = %q %v", got, err)
	}
	restore()
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	got, err = Relaunch(readable, "CAROL3", Request{SessionID: "s1"})
	if !errors.Is(err, ErrUnavailable) || got != "" {
		t.Fatalf("no chooser: Relaunch = %q %v", got, err)
	}
}

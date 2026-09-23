package artifacts

import (
	"errors"
	"testing"
)

func TestIdentityIsTrimmedAndCached(t *testing.T) {
	calls := 0
	stubCommand(t, func(string) (string, error) {
		calls++
		return "alice@example.test\n", nil
	})
	source := newIdentitySource("whoami")
	for range 3 {
		if got := source.get(); got != "alice@example.test" {
			t.Fatalf("got %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("ran the identity command %d times, want 1", calls)
	}
}

// gcloud answers "(unset)" rather than failing when no account is set,
// which would otherwise be recorded as a publisher's name.
func TestUnsetGcloudAccountIsNobody(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return "(unset)\n", nil })
	if got := newIdentitySource("gcloud config get-value account").get(); got != "" {
		t.Fatalf("recorded %q as a publisher", got)
	}
}

// A publisher that cannot be named does not stop the publish, and a login
// fixed mid-session names the next one.
func TestIdentityFailureIsEmptyAndNotCached(t *testing.T) {
	calls := 0
	stubCommand(t, func(string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("not logged in")
		}
		return "alice@example.test", nil
	})
	source := newIdentitySource("whoami")
	if got := source.get(); got != "" {
		t.Fatalf("a failed read produced %q", got)
	}
	if got := source.get(); got != "alice@example.test" {
		t.Fatalf("second read got %q", got)
	}
}

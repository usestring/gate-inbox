package artifacts

import (
	"errors"
	"strings"
	"testing"
)

func stubCommand(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	previous := runCommand
	runCommand = fn
	t.Cleanup(func() { runCommand = previous })
}

func TestKeyCommandSubstitutesTheSecretName(t *testing.T) {
	var got string
	stubCommand(t, func(command string) (string, error) {
		got = command
		return "key", nil
	})
	source := newKeySource("read --secret={secret} --project=p", "ARTIFACT_KEY")
	if _, err := source.get(); err != nil {
		t.Fatalf("get: %v", err)
	}
	if want := "read --secret=ARTIFACT_KEY --project=p"; got != want {
		t.Fatalf("ran %q, want %q", got, want)
	}
}

// A key stored with a trailing newline signs differently from the same key
// without one. Both ends trim, so neither has to be stored perfectly; this
// is the end that would otherwise reject every link it minted.
func TestKeyIsTrimmed(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return "  the-key\n", nil })
	key, err := newKeySource("read {secret}", "S").get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(key) != "the-key" {
		t.Fatalf("got %q, want %q", key, "the-key")
	}
}

func TestKeyIsReadOnlyOnce(t *testing.T) {
	calls := 0
	stubCommand(t, func(string) (string, error) {
		calls++
		return "the-key", nil
	})
	source := newKeySource("read {secret}", "S")
	for range 3 {
		if _, err := source.get(); err != nil {
			t.Fatalf("get: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("read the secret %d times, want 1", calls)
	}
}

// The usual cause of a failure here is an expired login, and an operator
// who fixes it expects the next call to work rather than the next session.
func TestKeyFailureIsNotCached(t *testing.T) {
	calls := 0
	stubCommand(t, func(string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("reauthentication required")
		}
		return "the-key", nil
	})
	source := newKeySource("read {secret}", "S")
	if _, err := source.get(); err == nil {
		t.Fatal("wanted the first read to fail")
	}
	key, err := source.get()
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if string(key) != "the-key" {
		t.Fatalf("got %q", key)
	}
}

func TestEmptyKeyIsAnError(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return "\n  \n", nil })
	_, err := newKeySource("read {secret}", "ARTIFACT_KEY").get()
	if err == nil {
		t.Fatal("an empty secret was accepted")
	}
	if !strings.Contains(err.Error(), "ARTIFACT_KEY") {
		t.Fatalf("error does not name the secret: %v", err)
	}
}

// The failure names the secret, because "permission denied" without it
// sends an operator looking in the wrong place.
func TestKeyErrorNamesTheSecret(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return "", errors.New("permission denied") })
	_, err := newKeySource("read {secret}", "ARTIFACT_KEY").get()
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_KEY") {
		t.Fatalf("error does not name the secret: %v", err)
	}
}

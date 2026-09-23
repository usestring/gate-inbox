package accounts

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func stub(t *testing.T, fn func(command string) (string, error)) {
	t.Helper()
	prev := run
	run = fn
	t.Cleanup(func() { run = prev })
}

var claude = config.Tool{
	AccountEnv:      "CLAUDE_CODE_OAUTH_TOKEN",
	AccountSecret:   "CLAUDE_OAUTH_TOKEN_{account}",
	AccountCommand:  "read {secret}",
	AccountsCommand: "list",
}

// The account is spelled the way the secret is, and a name that could not be
// a secret name never reaches the shell.
func TestSecretSpellsTheAccountAndRefusesAnythingElse(t *testing.T) {
	got, err := Secret(claude, " alice1 ")
	if err != nil || got != "CLAUDE_OAUTH_TOKEN_ALICE1" {
		t.Fatalf("secret = %q, %v", got, err)
	}
	for _, bad := range []string{"", "a; rm -rf /"} {
		if _, err := Secret(claude, bad); err == nil {
			t.Errorf("Secret accepted %q", bad)
		}
	}
	if _, err := Resolve(config.Tool{}, "ALICE1"); err == nil {
		t.Error("a tool with no recipe resolved an account")
	}
}

// Resolve reads the secret through the configured command and refuses an
// empty or failed read by the account's name, so the launch it refuses says
// which account was in play.
func TestResolveReadsTheSecretThroughTheCommand(t *testing.T) {
	ran := ""
	stub(t, func(command string) (string, error) {
		ran = command
		return "sk-ant-oat01-token\n", nil
	})
	got, err := Resolve(claude, "bob2")
	if err != nil || got != "sk-ant-oat01-token" || ran != "read CLAUDE_OAUTH_TOKEN_BOB2" {
		t.Fatalf("token = %q, ran %q, %v", got, ran, err)
	}
	stub(t, func(string) (string, error) { return "\n", nil })
	if _, err := Resolve(claude, "ALICE1"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty secret: %v", err)
	}
	stub(t, func(string) (string, error) { return "", errors.New("denied") })
	if _, err := Resolve(claude, "ALICE1"); err == nil || !strings.Contains(err.Error(), "ALICE1") || !strings.Contains(err.Error(), "denied") {
		t.Errorf("failed read: %v", err)
	}
}

// List reports accounts by name, whether the command printed bare names or
// resource paths, and drops secrets that are not accounts at all.
func TestListReportsAccountsByName(t *testing.T) {
	stub(t, func(string) (string, error) {
		return "projects/1/secrets/CLAUDE_OAUTH_TOKEN_ALICE1\nCLAUDE_OAUTH_TOKEN_BOB2\nANTHROPIC_API_KEY\n\n", nil
	})
	got, err := List(claude)
	if err != nil || strings.Join(got, ",") != "ALICE1,BOB2" {
		t.Fatalf("names = %v, %v", got, err)
	}
	if _, err := List(config.Tool{}); err == nil {
		t.Error("a tool with no accounts_command listed accounts")
	}
}

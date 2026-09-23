// Package accounts resolves a named subscription into the token a session
// runs on. The names are the operator's own for the accounts the team
// pools ("ALICE1", "BOB2"), and each one is a secret in a secret store;
// the token itself is read at launch and never stored, so a row only ever
// says which account it was on.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

// timeout bounds the secret read. It is one network call, and a caller
// waiting on it is a caller whose session has not launched yet.
const timeout = 30 * time.Second

// namePattern is the shape an account name may take: the secret name is
// built from it and handed to a shell, so it is kept to what a secret name
// can be made of rather than quoted.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// run executes a resolved command line and returns its stdout, replaced in
// tests so nothing here reaches a real secret store.
var run = func(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("no answer within %s", timeout)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// Normalize is the account name as the secret is spelled: upper case, so
// "alice1" and "ALICE1" are one account rather than one that resolves and
// one that does not.
func Normalize(name string) string {
	return strings.ToUpper(strings.TrimSpace(name))
}

// Secret is the secret name holding one account's token for a tool.
func Secret(tool config.Tool, account string) (string, error) {
	account = Normalize(account)
	if !namePattern.MatchString(account) {
		return "", fmt.Errorf("account %q is not a name: letters, digits, - and _ only", account)
	}
	if tool.AccountSecret == "" {
		return "", errors.New("no account_secret configured for this tool")
	}
	return strings.ReplaceAll(tool.AccountSecret, "{account}", account), nil
}

// Resolve reads one account's token for a tool. It is called at launch and
// the value goes straight into the session's environment; a name that will
// not resolve refuses the launch rather than starting a session on whatever
// login the CLI finds for itself.
func Resolve(tool config.Tool, account string) (string, error) {
	secret, err := Secret(tool, account)
	if err != nil {
		return "", err
	}
	if tool.AccountCommand == "" {
		return "", errors.New("no account_command configured for this tool")
	}
	out, err := run(strings.ReplaceAll(tool.AccountCommand, "{secret}", secret))
	if err != nil {
		return "", fmt.Errorf("account %s: read %s: %w", Normalize(account), secret, err)
	}
	token := strings.TrimSpace(out)
	if token == "" {
		return "", fmt.Errorf("account %s: secret %s is empty", Normalize(account), secret)
	}
	return token, nil
}

// List names the accounts a tool can be launched on, read from the secrets
// its AccountsCommand prints: each line is a secret name, and the ones that
// fit AccountSecret's shape are reported by the account name inside them.
// A line that does not fit is somebody else's secret and is dropped.
func List(tool config.Tool) ([]string, error) {
	if tool.AccountsCommand == "" {
		return nil, errors.New("no accounts_command configured for this tool")
	}
	prefix, suffix, ok := strings.Cut(tool.AccountSecret, "{account}")
	if !ok {
		return nil, errors.New("account_secret must contain {account}")
	}
	out, err := run(tool.AccountsCommand)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		// A secret store may print a full resource path or a bare name; the
		// secret name is the last segment either way.
		secret := strings.TrimSpace(line)
		if i := strings.LastIndex(secret, "/"); i >= 0 {
			secret = secret[i+1:]
		}
		if secret == "" || !strings.HasPrefix(secret, prefix) || !strings.HasSuffix(secret, suffix) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(secret, prefix), suffix)
		if name == "" || !namePattern.MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

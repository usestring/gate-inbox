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
	"sync"
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

// ErrUnavailable is an account whose secret cannot be read at all: its
// latest version is disabled or destroyed, the secret is gone, or the
// operator may not read it. Every *UnavailableError matches it. It is not a
// read that timed out or a secret store that could not be reached, which
// say nothing about the account and may answer on the next try.
var ErrUnavailable = errors.New("account unavailable")

// UnavailableError is one account whose secret cannot be read. Its message
// is one line meant for the operator; Err keeps the secret store's own
// answer for anything that wants it.
type UnavailableError struct {
	Account string
	Secret  string
	// Reason says why in a few words: "its secret version is disabled".
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("account %s is unavailable: %s", e.Account, e.Reason)
}

func (e *UnavailableError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrUnavailable}
	}
	return []error{ErrUnavailable, e.Err}
}

// reasonRefused is the UnavailableError reason for a read the secret store
// refused this operator.
const reasonRefused = "you may not read its secret"

// unavailableReason reads a failed secret read's error for the secret
// store's own verdict on the secret, by the status words Google Secret
// Manager (gcloud) prints. Anything else is not taken for the account's
// fault: a shell's "command not found" or a timeout must not hide an
// account from the picker.
func unavailableReason(err error) (string, bool) {
	text := err.Error()
	switch {
	case strings.Contains(text, "DISABLED state"):
		return "its secret version is disabled", true
	case strings.Contains(text, "DESTROYED state"):
		return "its secret version is destroyed", true
	case strings.Contains(text, "NOT_FOUND"):
		return "its secret or secret version does not exist", true
	case strings.Contains(text, "PERMISSION_DENIED"):
		return reasonRefused, true
	}
	return "", false
}

// unavailableFor is how long an account found unavailable stays out of List
// and away from the chooser in this process. Resolve always reads, so an
// account somebody names comes back the moment its secret does; the mark
// only stops the board offering it in the meantime.
const unavailableFor = 10 * time.Minute

// prefetchFor is how long a token read to check a chosen account waits for
// the launch it was read for, so a launch reads its secret once.
const prefetchFor = time.Minute

var marks = struct {
	sync.Mutex
	unavailable map[string]time.Time
	prefetched  map[string]prefetch
	now         func() time.Time
}{unavailable: map[string]time.Time{}, prefetched: map[string]prefetch{}, now: time.Now}

type prefetch struct {
	token string
	at    time.Time
}

// markAvailable records what a read of secret found: unavailable, or not.
func markAvailable(secret string, available bool) {
	marks.Lock()
	defer marks.Unlock()
	if available {
		delete(marks.unavailable, secret)
		return
	}
	marks.unavailable[secret] = marks.now()
}

// MarkedUnavailable says this process found the account's secret
// unreadable within the last unavailableFor.
func MarkedUnavailable(tool config.Tool, account string) bool {
	secret, err := Secret(tool, account)
	if err != nil {
		return false
	}
	return markedUnavailable(secret)
}

func markedUnavailable(secret string) bool {
	marks.Lock()
	defer marks.Unlock()
	at, ok := marks.unavailable[secret]
	if ok && marks.now().Sub(at) >= unavailableFor {
		delete(marks.unavailable, secret)
		return false
	}
	return ok
}

// takePrefetched is a token Check read for secret moments ago, handed over
// once.
func takePrefetched(secret string) (string, bool) {
	marks.Lock()
	defer marks.Unlock()
	p, ok := marks.prefetched[secret]
	delete(marks.prefetched, secret)
	if !ok || marks.now().Sub(p.at) >= prefetchFor {
		return "", false
	}
	return p.token, true
}

// Resolve reads one account's token for a tool. It is called at launch and
// the value goes straight into the session's environment; a name that will
// not resolve refuses the launch rather than starting a session on whatever
// login the CLI finds for itself. A secret that cannot be read at all is an
// *UnavailableError, matching ErrUnavailable.
func Resolve(tool config.Tool, account string) (string, error) {
	secret, err := Secret(tool, account)
	if err != nil {
		return "", err
	}
	if token, ok := takePrefetched(secret); ok {
		return token, nil
	}
	return read(tool, account, secret)
}

// Check reads the account's token to learn whether a launch on it can go
// ahead, and keeps it briefly for that launch's Resolve. It returns what
// Resolve would.
func Check(tool config.Tool, account string) error {
	secret, err := Secret(tool, account)
	if err != nil {
		return err
	}
	token, err := read(tool, account, secret)
	if err != nil {
		return err
	}
	marks.Lock()
	defer marks.Unlock()
	now := marks.now()
	// A launch that never came leaves its token behind; it is held no
	// longer than prefetchFor either way.
	for other, p := range marks.prefetched {
		if now.Sub(p.at) >= prefetchFor {
			delete(marks.prefetched, other)
		}
	}
	marks.prefetched[secret] = prefetch{token: token, at: now}
	return nil
}

func read(tool config.Tool, account, secret string) (string, error) {
	if tool.AccountCommand == "" {
		return "", errors.New("no account_command configured for this tool")
	}
	name := Normalize(account)
	out, err := run(strings.ReplaceAll(tool.AccountCommand, "{secret}", secret))
	if err != nil {
		if reason, ok := unavailableReason(err); ok {
			markAvailable(secret, false)
			return "", &UnavailableError{Account: name, Secret: secret, Reason: reason, Err: err}
		}
		return "", fmt.Errorf("account %s: read %s: %w", name, secret, err)
	}
	token := strings.TrimSpace(out)
	if token == "" {
		markAvailable(secret, false)
		return "", &UnavailableError{Account: name, Secret: secret, Reason: "its secret is empty"}
	}
	markAvailable(secret, true)
	return token, nil
}

// List names the accounts a tool can be launched on, read from the secrets
// its AccountsCommand prints: each line is a secret name, and the ones that
// fit AccountSecret's shape are reported by the account name inside them.
// A line that does not fit is somebody else's secret and is dropped, and so
// is an account this process found unavailable lately (MarkedUnavailable):
// listing reads no secret, so it learns that only from a launch's read.
func List(tool config.Tool) ([]string, error) {
	return list(tool, run)
}

// list is List reading the command's output through read, which is run
// itself or the board's cached listing.
func list(tool config.Tool, read func(string) (string, error)) ([]string, error) {
	if tool.AccountsCommand == "" {
		return nil, errors.New("no accounts_command configured for this tool")
	}
	prefix, suffix, ok := strings.Cut(tool.AccountSecret, "{account}")
	if !ok {
		return nil, errors.New("account_secret must contain {account}")
	}
	out, err := read(tool.AccountsCommand)
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
		if name == "" || !namePattern.MatchString(name) || markedUnavailable(prefix+Normalize(name)+suffix) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

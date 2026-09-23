package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// keyTimeout bounds one secret read. It is a single network call on the
// first publish of a session, not on the launch path.
const keyTimeout = 30 * time.Second

// runCommand executes a resolved command line and returns its stdout. It is
// a variable so tests never reach a real secret store, matching how
// internal/accounts resolves an account token.
//
// Deliberately not shared with that package: the two read different secrets
// for different reasons, and the twenty lines they have in common are not
// worth a dependency between a subscription token and an artifact key.
var runCommand = func(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keyTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("no answer within %s", keyTimeout)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// keySource resolves the signing key on demand and keeps it in memory for
// the life of the process.
//
// Lazily, because Enabled runs at the start of every session the manager
// spawns and a secret read there would put a network call on the launch
// path of every agent. The cost is that a key the operator cannot read
// surfaces when a tool is called rather than when it is registered.
//
// A failure is not cached. The usual cause is an expired login, and an
// operator who fixes it expects the next call to work rather than the next
// session.
type keySource struct {
	command string
	secret  string

	mu  sync.Mutex
	key []byte
}

func newKeySource(command, secret string) *keySource {
	return &keySource{command: command, secret: secret}
}

// resolved is the command with {secret} filled in.
func (k *keySource) resolved() string {
	return strings.ReplaceAll(k.command, "{secret}", k.secret)
}

func (k *keySource) get() ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.key) > 0 {
		return k.key, nil
	}
	out, err := runCommand(k.resolved())
	if err != nil {
		return nil, fmt.Errorf("cannot read the artifact signing key from %s: %w", k.secret, err)
	}
	// Both ends trim, so a key stored with a trailing newline still signs
	// identically. Without it the worker refuses every request while
	// looking like it works.
	key := strings.TrimSpace(out)
	if key == "" {
		return nil, fmt.Errorf("the artifact signing key in %s is empty", k.secret)
	}
	k.key = []byte(key)
	return k.key, nil
}

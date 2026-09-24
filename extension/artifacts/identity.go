package artifacts

import (
	"strings"
	"sync"
)

// identitySource reports who is publishing, so the team can see who made
// what. It is read lazily and kept for the life of the process, like the
// key, and for the same reason: Enabled runs on every session's launch path.
//
// Unlike the key, failing to read it does not stop a publish. An artifact
// with no name on it is less useful than one with; an artifact that could
// not be published at all because a name could not be found is worse.
type identitySource struct {
	command string

	mu    sync.Mutex
	value string
}

func newIdentitySource(command string) *identitySource {
	return &identitySource{command: command}
}

// get returns the publisher, or "" when it cannot be read. A failure is not
// cached, so a login fixed mid-session names the next artifact.
func (i *identitySource) get() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.value != "" {
		return i.value
	}
	if strings.TrimSpace(i.command) == "" {
		return ""
	}
	out, err := runCommand(i.command)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(out)
	// gcloud prints "(unset)" rather than failing when no account is
	// configured, which would otherwise be recorded as someone's name.
	if value == "(unset)" {
		return ""
	}
	i.value = value
	return value
}

package tracing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The Axiom credential can be fetched on the first send rather than read from
// the environment, so that turning tracing on need not mean putting a token in
// a shell profile, a process's argv, or a shell history file -- the three
// places a credential leaks from without anyone deciding to leak it.
const (
	// SecretEnv names the Secret Manager secret holding the ingest token, and
	// ProjectEnv the project holding it. Neither has a default: which secret
	// in which project is the operator's to name, and a guess would be a
	// credential read nobody asked for.
	SecretEnv  = "GATE_INBOX_TRACE_SECRET"
	ProjectEnv = "GATE_INBOX_TRACE_PROJECT"

	// secretTimeout bounds the fetch. It runs before the TUI takes the
	// terminal, so a gcloud that is hanging on a network it cannot reach
	// would otherwise look exactly like a manager that will not start.
	secretTimeout = 30 * time.Second
)

// resolveToken produces the Axiom ingest token.
//
// A token in TokenEnv wins and costs nothing: that is the path for a machine
// with no gcloud, and for a test. Otherwise it comes from the Secret Manager
// secret SecretEnv and ProjectEnv name, read with whatever identity gcloud
// already holds -- a user's application-default credentials, or a service
// account -- and the caller has to know about neither. With neither named
// there is no token, and nothing is read.
//
// gcloud does the reading rather than the Go client library. Application
// default credentials come in several shapes -- an authorized user, a service
// account key, an external account, an impersonation chain -- and gcloud is
// what already resolves all of them, and what every other secret read on this
// box goes through. The client library would also pull in grpc and protobuf,
// which this package went out of its way not to depend on.
func resolveToken() (string, error) {
	if token := os.Getenv(TokenEnv); token != "" {
		return token, nil
	}
	secret := strings.TrimSpace(os.Getenv(SecretEnv))
	project := strings.TrimSpace(os.Getenv(ProjectEnv))
	if secret == "" || project == "" {
		return "", fmt.Errorf("no axiom token: set %s, or name its secret with %s and %s", TokenEnv, SecretEnv, ProjectEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), secretTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gcloud", "secrets", "versions", "access", "latest",
		"--secret="+secret, "--project="+project).Output()
	if err != nil {
		// gcloud says why on stderr -- unauthenticated, no such secret, no
		// permission -- and that sentence is the whole fix, so it is carried
		// rather than replaced with a status code.
		return "", fmt.Errorf("read %s from Secret Manager in %s: %w: %s",
			secret, project, err, firstLine(stderrOf(err)))
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("%s in %s is empty", secret, project)
	}
	return token, nil
}

func stderrOf(err error) []byte {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.Stderr
	}
	return nil
}

func firstLine(out []byte) string {
	text := strings.TrimSpace(string(out))
	if line, _, cut := strings.Cut(text, "\n"); cut {
		return line
	}
	return text
}

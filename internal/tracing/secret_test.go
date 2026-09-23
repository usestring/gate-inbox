package tracing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubGcloud puts a gcloud on PATH that records how it was called and answers
// with script. The production code keeps no seam for this: what is under test
// is the real exec, including the argument shape, and a fake injected through
// a field would stop testing that the moment the arguments changed.
func stubGcloud(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gcloud"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// namedSecret names the secret the way an operator does. Nothing is named
// by default, so every test that reads one has to.
func namedSecret(t *testing.T) {
	t.Helper()
	t.Setenv(TokenEnv, "")
	t.Setenv(SecretEnv, "TRACE_TOKEN")
	t.Setenv(ProjectEnv, "trace-project")
}

// The whole point of reading the secret is that the token never has to exist
// in an environment a profile or a history file can keep.
func TestTheTokenIsReadFromSecretManager(t *testing.T) {
	namedSecret(t)
	calls := stubGcloud(t, `echo "xaat-from-secret-manager"`)

	token, err := resolveToken()
	if err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if token != "xaat-from-secret-manager" {
		t.Fatalf("token %q: the secret's value did not come back, or was not trimmed", token)
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("gcloud was never run, so the token came from somewhere else: %v", err)
	}
	for _, want := range []string{"secrets versions access latest", "--secret=TRACE_TOKEN", "--project=trace-project"} {
		if !strings.Contains(string(recorded), want) {
			t.Fatalf("gcloud was called as %q, missing %q", strings.TrimSpace(string(recorded)), want)
		}
	}
}

// A machine with no gcloud, and a test, still need a way in.
func TestATokenInTheEnvironmentWinsAndCostsNothing(t *testing.T) {
	t.Setenv(TokenEnv, "xaat-literal")
	calls := stubGcloud(t, `echo "xaat-from-secret-manager"`)

	token, err := resolveToken()
	if err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if token != "xaat-literal" {
		t.Fatalf("token %q, want the literal one from the environment", token)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("gcloud ran even though the token was already in hand")
	}
}

// No secret is guessed. Without a token, a secret and a project all named,
// there is nothing to read, and gcloud is never run to find one.
func TestNoSecretIsReadUnlessOneIsNamed(t *testing.T) {
	for name, env := range map[string][2]string{
		"neither":      {"", ""},
		"secret only":  {"TRACE_TOKEN", ""},
		"project only": {"", "trace-project"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(TokenEnv, "")
			t.Setenv(SecretEnv, env[0])
			t.Setenv(ProjectEnv, env[1])
			calls := stubGcloud(t, `echo "xaat-guessed"`)

			if token, err := resolveToken(); err == nil {
				t.Fatalf("resolveToken found %q with no secret named", token)
			}
			if _, err := os.Stat(calls); err == nil {
				t.Fatal("gcloud ran to look for a secret nobody named")
			}
		})
	}
}

// gcloud's own sentence is the fix -- "not authenticated", "secret not found",
// "permission denied" -- so losing it for an exit status would leave an
// operator with a manager that will not start and no idea why.
func TestAFailedReadCarriesWhatGcloudSaid(t *testing.T) {
	namedSecret(t)
	stubGcloud(t, `echo "ERROR: (gcloud.secrets.versions.access) NOT_FOUND: Secret [nope] not found." >&2; exit 1`)

	_, err := resolveToken()
	if err == nil {
		t.Fatal("a failed read was reported as success")
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Fatalf("error %q dropped gcloud's explanation", err)
	}
}

// An empty secret would otherwise become an Authorization header of "Bearer ",
// and Axiom's 401 is a much longer way round to the same answer.
func TestAnEmptySecretIsRefused(t *testing.T) {
	namedSecret(t)
	stubGcloud(t, `printf ""`)

	if _, err := resolveToken(); err == nil {
		t.Fatal("an empty secret was accepted as a token")
	}
}

// The dataset has no default: asking for axiom without naming one is refused
// at startup, where a person sees it, rather than sent somewhere guessed.
func TestTheDatasetMustBeNamed(t *testing.T) {
	t.Setenv(TokenEnv, "xaat-literal")
	t.Setenv(DatasetEnv, "")
	tracer, err := Start("axiom")
	if err == nil {
		tracer.Close()
		t.Fatalf("Start(axiom) with no dataset named sent to %q", tracer.Where())
	}
	if !strings.Contains(err.Error(), DatasetEnv) {
		t.Fatalf("the refusal %q does not say what to set", err)
	}

	t.Setenv(DatasetEnv, "somewhere-else")
	other, err := Start("axiom")
	if err != nil {
		t.Fatalf("Start(axiom): %v", err)
	}
	t.Cleanup(func() { other.Close() })
	if !strings.Contains(other.Where(), "somewhere-else") {
		t.Fatalf("a named dataset was ignored: %q", other.Where())
	}
}

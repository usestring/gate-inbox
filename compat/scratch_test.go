package compat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
)

// recording is read once, before any test empties the environment.
var recording = envname.Get(envname.Golden) == "write"

// golden compares got with testdata/name, or records it under
// GATE_INBOX_GOLDEN=write.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if recording {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; record it with %s=write go test ./compat/", err, envname.Golden)
	}
	if got == string(want) {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s differs from line %d (%d lines now, %d recorded)\n got: %q\nwant: %q\n"+
				"re-record with %s=write only if this change is meant",
				path, i+1, len(gotLines), len(wantLines), g, w, envname.Golden)
		}
	}
}

// scratch is one test's whole world: a board home, a tmux socket
// directory, a user home, and a PATH holding only fake CLIs.
type scratch struct {
	root string
	// home is GATE_INBOX_HOME.
	home string
	bin  string
	// callLog collects every argv the fake secret store was run with.
	callLog string
}

// synthetic identities and credentials. None of them names a real account.
const (
	fakeBorrower   = "ada"
	fakeSecretStem = "EXAMPLE_TOKEN_"
	fakeTokenStem  = "fake-token-for-"
)

// fakeCLIs are the agent CLIs the default config names, and gh, each a
// stub that answers --version and otherwise exits 0 without doing anything.
var fakeCLIs = []string{"claude", "codex", "opencode", "gh"}

// hostTools are the only host binaries a scratch PATH carries, resolved
// before any test empties the environment: the account reader runs its
// command through sh, and the synthetic key and identity commands are
// printf. tmux is deliberately not among them, so nothing a test runs can
// reach or start a tmux server.
var hostTools = func() map[string]string {
	tools := map[string]string{}
	for _, name := range []string{"sh", "printf"} {
		tools[name], _ = exec.LookPath(name)
	}
	return tools
}()

// newScratch builds the scratch world and switches the process environment
// to it for the rest of the test. The environment is emptied first, so
// nothing the host exported -- an operator's token, their tmux pane, their
// board home -- can leak into a launch plan or a golden.
func newScratch(t *testing.T) *scratch {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	s := &scratch{
		root:    root,
		home:    filepath.Join(root, "home"),
		bin:     filepath.Join(root, "bin"),
		callLog: filepath.Join(root, "calls.log"),
	}
	for _, dir := range []string{s.home, s.bin, filepath.Join(root, "user"), filepath.Join(root, "tmux"), filepath.Join(s.home, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range fakeCLIs {
		version := "1.0.0"
		if name == "opencode" {
			version = "2.4.0"
		}
		s.writeExecutable(t, filepath.Join(s.bin, name),
			"#!/bin/sh\nif [ \"$1\" = --version ]; then echo "+version+"; fi\nexit 0\n")
	}
	s.writeExecutable(t, filepath.Join(s.bin, "secrets"), fakeSecrets(s.callLog))
	// The installed copy launch.Executable points sessions at.
	s.writeExecutable(t, filepath.Join(s.home, "bin", app.Name), "#!/bin/sh\nexit 0\n")
	for name, path := range hostTools {
		if path == "" {
			t.Fatalf("%s is not on this host's PATH", name)
		}
		if err := os.Symlink(path, filepath.Join(s.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	isolateEnv(t, s.env())
	return s
}

// env is the whole environment a scratch process sees. Three variables
// stand in for what an operator's shell carries: an ordinary export, which
// a session inherits; the tmux pane and the Claude Code stamp, which it
// must not; and a key, which it inherits and a golden must never print.
func (s *scratch) env() []string {
	return []string{
		"PATH=" + s.bin,
		"HOME=" + filepath.Join(s.root, "user"),
		config.HomeEnv + "=" + s.home,
		"TMUX_TMPDIR=" + filepath.Join(s.root, "tmux"),
		"OPERATOR_EXPORT=inherited",
		"TMUX_PANE=%99",
		"CLAUDECODE=1",
		"EXAMPLE_API_KEY=fake-inherited-key",
	}
}

func (s *scratch) writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (s *scratch) writeFile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.home, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSecrets is the secret store accountConfig points claude at: it lists
// three synthetic account tokens and one secret that is not an account,
// reads each token as a fake value, and logs each argv.
func fakeSecrets(log string) string {
	return `#!/bin/sh
echo "secrets $*" >> '` + log + `'
case "$1" in
list)
	echo ` + fakeSecretStem + `ADA1
	echo ` + fakeSecretStem + `BOB1
	echo ` + fakeSecretStem + `CAT1
	echo UNRELATED_SECRET
	;;
read)
	case "$2" in
	` + fakeSecretStem + `*) echo "` + fakeTokenStem + `$2" ;;
	*) exit 1 ;;
	esac
	;;
*)
	exit 2
	;;
esac
`
}

// accountConfig gives claude a synthetic account setup on the fake secret
// store. The built-in config names no secret store, so without it every
// account scenario would record an empty pool.
const accountConfig = `[tools.claude]
account_env = "CLAUDE_CODE_OAUTH_TOKEN"
account_secret = "` + fakeSecretStem + `{account}"
account_command = "secrets read {secret}"
accounts_command = "secrets list"
`

// isolateEnv replaces the process environment with env until the test
// ends. Tests here never run in parallel for that reason.
func isolateEnv(t *testing.T, env []string) {
	t.Helper()
	saved := os.Environ()
	os.Clearenv()
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		os.Setenv(key, value)
	}
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			key, value, _ := strings.Cut(kv, "=")
			os.Setenv(key, value)
		}
	})
}

var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// redact makes text independent of where and when it was produced: the
// scratch root becomes <scratch> and a freshly minted id becomes <uuid>.
func (s *scratch) redact(text string) string {
	text = strings.ReplaceAll(text, s.root, "<scratch>")
	return uuidPattern.ReplaceAllString(text, "<uuid>")
}

// secretName is a variable a golden prints as <redacted> rather than by
// value, whatever it holds.
var secretName = regexp.MustCompile(`(?i)(token|secret|key|password|credential)`)

// flatten renders a value as one "path = value" line per leaf, keyed by
// its toml names, with maps in sorted order. It is how a resolved config
// becomes a golden a reviewer can read one setting at a time.
func flatten(prefix string, v reflect.Value, out map[string]string) {
	if v.Type() == reflect.TypeOf(config.Duration{}) {
		out[prefix] = strconv.Quote(time.Duration(v.Field(0).Int()).String())
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			out[prefix] = "<unset>"
			return
		}
		flatten(prefix, v.Elem(), out)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			flatten(join(prefix, name), v.Field(i), out)
		}
	case reflect.Map:
		if v.Len() == 0 {
			out[prefix] = "{}"
			return
		}
		for _, key := range v.MapKeys() {
			flatten(join(prefix, key.String()), v.MapIndex(key), out)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			out[prefix] = "[]"
			return
		}
		if leaf(v.Index(0)) {
			items := make([]string, v.Len())
			for i := range items {
				items[i] = scalar(v.Index(i))
			}
			out[prefix] = "[" + strings.Join(items, ", ") + "]"
			return
		}
		for i := 0; i < v.Len(); i++ {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), v.Index(i), out)
		}
	default:
		out[prefix] = scalar(v)
	}
}

func leaf(v reflect.Value) bool {
	for v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Pointer:
		return false
	}
	return true
}

// scalar reads through reflect's kind accessors rather than Interface, so
// it can also print a field an extension keeps unexported.
func scalar(v reflect.Value) string {
	for v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	}
	return fmt.Sprintf("<%s>", v.Kind())
}

func join(prefix, name string) string {
	if strings.ContainsAny(name, ". ") {
		name = strconv.Quote(name)
	}
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

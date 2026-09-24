package extension_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/usestring/gate-inbox"

// helpersFixture is a main in a module of its own that uses every helper
// package under extension/.
var helpersFixture = filepath.Join("testdata", "helpers", "main.go")

func TestHelpersFixtureImportsOnlyPublicPackages(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), helpersFixture, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if strings.HasPrefix(path, modulePath) && !strings.HasPrefix(path, modulePath+"/extension/") {
			t.Errorf("the fixture imports %s, outside the extension helper packages", path)
		}
		if strings.Contains(path, "/internal") {
			t.Errorf("the fixture imports %s", path)
		}
	}
}

// TestHelpersBuildInAnotherModule builds the fixture as its own module,
// requiring this one through a replace. The go tool refuses an internal
// import across modules, so the build succeeding proves each helper group is
// usable from outside; each subtest then checks one group's answers.
func TestHelpersBuildInAnotherModule(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source, err := os.ReadFile(helpersFixture)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.test/helpers\n\ngo 1.26\n\n" +
		"require " + modulePath + " v0.0.0\n\n" +
		"replace " + modulePath + " => " + root + "\n"
	for name, body := range map[string][]byte{"main.go": source, "go.mod": []byte(goMod), "go.sum": sum} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "helpers")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the fixture module does not build against the public helpers: %v\n%s", err, out)
	}

	for group, want := range map[string]string{
		"mcptool": "text=\"from a file\" err=<nil>\n" +
			"neither=pass prompt or prompt_file\n" +
			"readonly=true idempotent=true\n" +
			"result=*mcp.TextContent\n",
		"textfmt": "tail=\"the end of the turn\"\n" +
			"first=\"one\"\n" +
			"strip=\"ab\\n\"\n" +
			"age=1h\n" +
			"decline=true\n" +
			"oneline=\"a b\"\n" +
			"wrap=[\"two\" \"word\" \"s\"]\n" +
			"fingerprint=true\n" +
			"width=4 cut=\"abc…\"\n",
		"cmdline": "{\n  \"armed\": \"ab12\"\n}\n" +
			"dispatch=[\"ab12\" \"true\"] err=<nil>\n" +
			"state=<nil>\n",
		"gitroot": "root=/r err=<nil>\n" +
			"within=true outside=false\n",
	} {
		t.Run(group, func(t *testing.T) {
			run := exec.Command(bin, group)
			run.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
			out, err := run.CombinedOutput()
			if err != nil || string(out) != want {
				t.Fatalf("%s: %v\ngot:\n%s\nwant:\n%s", group, err, out, want)
			}
		})
	}
}

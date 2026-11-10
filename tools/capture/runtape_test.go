package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// absolutePath is a filesystem path under a root a machine keeps local state
// in. A tape on a public PR is scanned for local paths, and the scratch
// directories a capture runs in are local paths like any other. A bare
// "/segment/" is not enough: fixture URLs carry those.
var absolutePath = regexp.MustCompile(`(^|[\s'"=:(\x60])/(tmp|home|Users|private|var|root|opt|etc|mnt|run|Volumes)(/|\b)`)

func runTape(t *testing.T, scenario string, tools ...string) string {
	t.Helper()
	args := append([]string{"runtape.sh", scenario, "media/run", "test-results/run"}, tools...)
	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(), "CAP_WIDTH=1400", "CAP_HEIGHT=800", "CAP_FONT_SIZE=20",
		`CAP_VHS_THEME_JSON={ "name": "oled", "background": "#000000" }`)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("runtape.sh %s: %v\n%s", scenario, err, stderr.String())
	}
	return string(out)
}

// Every scenario in the repository makes a tape with no absolute path in it,
// whatever placeholders it uses or comments it carries.
func TestRunTapesNameNoAbsolutePath(t *testing.T) {
	scenarios, err := filepath.Glob("scenarios/*.tape")
	if err != nil || len(scenarios) == 0 {
		t.Fatalf("no scenarios found: %v", err)
	}
	for _, scenario := range scenarios {
		t.Run(filepath.Base(scenario), func(t *testing.T) {
			tape := runTape(t, scenario, "claude", "codex")
			for i, line := range strings.Split(tape, "\n") {
				if loc := absolutePath.FindStringIndex(line); loc != nil {
					t.Errorf("line %d names an absolute path: %s", i+1, line)
				}
			}
			if strings.Contains(tape, "{{") {
				t.Errorf("a placeholder was left in the tape:\n%s", tape)
			}
		})
	}
}

// The placeholders become names the capture's shell resolves, and a
// scenario's comments stay out of the tape.
func TestRunTapeReplacesPlaceholdersWithNames(t *testing.T) {
	scenario := filepath.Join(t.TempDir(), "s.tape")
	body := "# usage: --config /tmp/scratch/x.toml\n" +
		"Type \"{{TMUX}} kill-server\"\nType \"{{LAUNCH}}\"\n" +
		"Type `python3 -c \"open('{{HOME}}/state.db')\"`\nScreenshot {{SHOTS}}/01.png\n"
	if err := os.WriteFile(scenario, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tape := runTape(t, scenario)
	for _, want := range []string{
		`Type "tmux -S $GIB_CAP_TMUX kill-server"`,
		`Type "clear && gib-cap-launch"`,
		"open('$GIB_CAP_HOME/state.db')",
		"Screenshot media/run/01.png",
	} {
		if !strings.Contains(tape, want) {
			t.Errorf("tape lacks %q:\n%s", want, tape)
		}
	}
	if strings.Contains(tape, "usage:") {
		t.Errorf("a scenario comment reached the tape:\n%s", tape)
	}
}

func TestRunTapeRefusesAnAbsoluteOutput(t *testing.T) {
	cmd := exec.Command("bash", "runtape.sh", "scenarios/board-tour.tape", "/tmp/media", "test-results/run")
	cmd.Env = append(os.Environ(), "CAP_WIDTH=1", "CAP_HEIGHT=1", "CAP_FONT_SIZE=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("runtape.sh accepted an absolute output directory")
	}
}

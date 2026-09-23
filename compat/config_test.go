package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/usestring/gate-inbox/internal/config"
)

const configLegend = `# Every setting the board resolves, one per line, marked with where its value came from:
#   file     written in config.toml and used as written
#   file*    written in config.toml, but the board uses a different value (the file's follows)
#   default  not in config.toml; filled from the built-in default
#   ignored  written in config.toml, but no setting reads it
`

// TestEffectiveConfig records the config the board runs on for each shape
// of config.toml it meets: none at all, an empty one, one an older release
// wrote, a minimal hand-written one and a full one.
func TestEffectiveConfig(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		input string // under testdata/input; "" writes no file at all
		empty bool
	}{
		{name: "absent"},
		{name: "empty", empty: true},
		{name: "old", input: "config-old.toml"},
		{name: "minimal", input: "config-minimal.toml"},
		{name: "full", input: "config-full.toml"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := newScratch(t)
			switch {
			case scenario.empty:
				s.writeFile(t, "config.toml", "")
			case scenario.input != "":
				raw, err := os.ReadFile(filepath.Join("testdata", "input", scenario.input))
				if err != nil {
					t.Fatal(err)
				}
				s.writeFile(t, "config.toml", string(raw))
			}
			cfg, err := config.LoadDir(s.home)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			written, err := os.ReadFile(filepath.Join(s.home, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			got := s.redact(describeConfig(t, cfg, string(written)))
			golden(t, "config/effective-"+scenario.name+".golden", got)
			if scenario.name == "absent" {
				// What a first run leaves on disk for the operator to edit.
				golden(t, "config/default-config.toml", string(written))
			}
		})
	}
}

func describeConfig(t *testing.T, cfg config.Config, written string) string {
	t.Helper()
	effective := map[string]string{}
	flatten("", reflect.ValueOf(cfg), effective)
	var raw map[string]any
	if _, err := toml.Decode(written, &raw); err != nil {
		t.Fatalf("decode the file as written: %v", err)
	}
	inFile := map[string]string{}
	if len(raw) > 0 {
		flatten("", reflect.ValueOf(raw), inFile)
	}

	var b strings.Builder
	b.WriteString(configLegend)
	for _, key := range sortedKeys(effective) {
		value := effective[key]
		fileValue, ok := inFile[key]
		switch {
		case !ok:
			fmt.Fprintf(&b, "default  %s = %s\n", key, value)
		case fileValue == value:
			fmt.Fprintf(&b, "file     %s = %s\n", key, value)
		default:
			fmt.Fprintf(&b, "file*    %s = %s\n         (file: %s)\n", key, value, fileValue)
		}
	}
	for _, key := range sortedKeys(inFile) {
		if _, ok := effective[key]; !ok && !coveredByEffective(key, effective) {
			fmt.Fprintf(&b, "ignored  %s = %s\n", key, inFile[key])
		}
	}
	return b.String()
}

// coveredByEffective reports whether a file key sits inside a table the
// config keeps whole, such as an extension's section, whose leaves the
// effective side flattens under the same path.
func coveredByEffective(key string, effective map[string]string) bool {
	for path := range effective {
		if strings.HasPrefix(path, key+".") || strings.HasPrefix(path, key+"[") {
			return true
		}
	}
	return false
}

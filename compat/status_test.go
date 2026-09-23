package compat

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

// statusFields are the Tool settings the status engine compiles, in the
// order it reads them.
var statusFields = []string{
	"status_source", "default_status", "type_ahead", "activity_cutoff", "input_line",
	"turn_end", "chrome_line", "blocked_line", "trailing_note", "busy_line", "limit_line",
	"arrow_dialog_line", "dialog_step_row", "dialog_step_entry", "scrolled_line",
	"jump_to_bottom_key", "echo_budget", "interrupt_keys",
}

// TestStatusPatterns records, per tool, the patterns the status engine
// derives a session's state from, as the default config resolves them, and
// checks the engine compiles all of them.
func TestStatusPatterns(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := status.NewEngine(cfg); err != nil {
		t.Fatalf("the default patterns do not compile: %v", err)
	}
	names := cfg.ToolNames()
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		tool := cfg.Tools[name]
		flat := map[string]string{}
		flatten("", reflect.ValueOf(tool), flat)
		fmt.Fprintf(&b, "[%s]\n", name)
		for _, field := range statusFields {
			fmt.Fprintf(&b, "%s = %s\n", field, flat[field])
		}
		for i := 0; ; i++ {
			state, ok := flat[fmt.Sprintf("rules[%d].state", i)]
			if !ok {
				break
			}
			fmt.Fprintf(&b, "rules[%d] %s %s\n", i, strings.Trim(state, `"`), flat[fmt.Sprintf("rules[%d].pattern", i)])
		}
		b.WriteString("\n")
	}
	golden(t, "status/patterns.golden", b.String())
}

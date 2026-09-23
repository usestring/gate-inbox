package status

import (
	"strings"
	"testing"
)

// The fixture keeps a live opencode 1.18.29 select prompt's structure byte for
// byte -- box border, spinner row of the still-open turn, option rows, the
// right-aligned status column and the legend -- with the captured session's
// question and option prose swapped for neutral text. The prose is the one part
// no rule reads; everything the rules turn on is as opencode drew it.
func TestOpencodeSelectPromptReadsWaiting(t *testing.T) {
	engine := defaultEngine(t)
	pane := loadPane(t, "opencode-question-select.txt")
	if got, _ := engine.Match("opencode", pane); got != Waiting {
		t.Fatalf("Match(opencode, select prompt) = %q want %q", got, Waiting)
	}
}

// The permission overlay steps its options with the horizontal arrows
// ("⇆ select"), so Left belongs to the pane there. A question dialog never
// does -- "↑↓ select", with "⇆ tab" only where there is something to tab
// between -- so Left is spare and leaves focus. The ⇆-tab variants below are
// the falsifiers: keying the arrow claim on a bare ⇆ would pin the operator
// in a dialog that is asking them something.
func TestOpencodeArrowOwnership(t *testing.T) {
	engine := defaultEngine(t)
	for _, name := range []string{
		"opencode-permission-bash.txt",
		"opencode-permission-edit.txt",
	} {
		t.Run(name, func(t *testing.T) {
			if !engine.DialogOwnsArrows("opencode", loadPane(t, name)) {
				t.Fatalf("DialogOwnsArrows(opencode, %s) = false, want true: Left steps the permission options", name)
			}
		})
	}
	base := loadPane(t, "opencode-question-select.txt")
	legends := []string{
		"↑↓ select  enter submit  esc dismiss",
		"⇆ tab  ↑↓ select  enter submit  esc dismiss",
		"↑↓ select  enter toggle  esc dismiss",
		"↑↓ select  enter confirm  esc dismiss",
		"⇆ tab  enter submit  esc dismiss",
		"enter confirm  esc dismiss",
	}
	for _, legend := range legends {
		t.Run(legend, func(t *testing.T) {
			pane := strings.Replace(base, "↑↓ select  enter submit  esc dismiss", legend, 1)
			if !strings.Contains(pane, legend) {
				t.Fatalf("the fixture no longer carries the legend this case swaps on")
			}
			if engine.DialogOwnsArrows("opencode", pane) {
				t.Fatalf("DialogOwnsArrows(opencode, question legend %q) = true, want false: Left is spare there", legend)
			}
		})
	}
}

// The prompt's legend drops its leading segments when the dialog has nothing to
// tab or step through, and names its own enter verb. Swapping just that row on
// the fixture pins every shape the component can draw, including the bare one
// where "enter <verb>  esc dismiss" is the whole line.
func TestOpencodeSelectPromptLegendVariants(t *testing.T) {
	engine := defaultEngine(t)
	base := loadPane(t, "opencode-question-select.txt")
	const drawn = "↑↓ select  enter submit  esc dismiss"
	if !strings.Contains(base, drawn) {
		t.Fatalf("fixture no longer carries the legend %q", drawn)
	}
	for _, legend := range []string{
		"⇆ tab  ↑↓ select  enter submit  esc dismiss",
		"↑↓ select  enter toggle  esc dismiss",
		"↑↓ select  enter confirm  esc dismiss",
		"⇆ tab  enter submit  esc dismiss",
		"enter confirm  esc dismiss",
	} {
		t.Run(legend, func(t *testing.T) {
			pane := strings.Replace(base, drawn, legend, 1)
			if got, _ := engine.Match("opencode", pane); got != Waiting {
				t.Fatalf("Match(opencode, %q) = %q want %q", legend, got, Waiting)
			}
		})
	}
}

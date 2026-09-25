package keymap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func resolve(t *testing.T, overrides Overrides) *Map {
	t.Helper()
	m, problems := New(overrides)
	for _, problem := range problems {
		t.Fatalf("unexpected problem: %s", problem)
	}
	return m
}

// The defaults are what shipped: nothing is unbound, no two actions on one
// screen hold the same key, and the map answers both ways.
func TestDefaultsResolveWithoutProblems(t *testing.T) {
	m := resolve(t, nil)
	for _, binding := range Catalog {
		if !m.Bound(binding.Context, binding.Action) {
			t.Errorf("%s.%s is unbound out of the box", binding.Context, binding.Action)
		}
		for _, key := range binding.Keys {
			action, ok := m.Action(binding.Context, key)
			if !ok || action != binding.Action {
				t.Errorf("%s on %s answers %q, want %s", key, binding.Context, action, binding.Action)
			}
		}
	}
}

// A key on two actions of one screen is a binding somebody would lose
// silently, so the catalog itself must never carry one.
func TestCatalogHasNoCollisions(t *testing.T) {
	held := map[Context]map[string]Action{}
	for _, binding := range Catalog {
		if held[binding.Context] == nil {
			held[binding.Context] = map[string]Action{}
		}
		for _, key := range binding.Keys {
			if owner, taken := held[binding.Context][key]; taken {
				t.Errorf("%s: %q is on both %s and %s", binding.Context, key, owner, binding.Action)
			}
			held[binding.Context][key] = binding.Action
		}
	}
}

func TestHelpAndLegendPeekHaveSeparateKeys(t *testing.T) {
	m := resolve(t, nil)
	for _, key := range []string{"H", "shift+h"} {
		action, ok := m.Action(ContextList, key)
		if !ok || action != Help {
			t.Errorf("%s on list answers %q, want %s", key, action, Help)
		}
	}
	if action, ok := m.Action(ContextList, "?"); !ok || action != LegendPeek {
		t.Errorf("? on list answers %q, want %s", action, LegendPeek)
	}
	for _, key := range []string{"H", "?", "shift+h"} {
		if action, ok := m.Action(ContextWelcome, key); !ok || action != Help {
			t.Errorf("%s on welcome answers %q, want %s", key, action, Help)
		}
	}
}

func TestOverrideMovesAKey(t *testing.T) {
	m := resolve(t, Overrides{ContextList: {NewSession: {"z"}}})
	if got := m.Key(ContextList, NewSession); got != "z" {
		t.Fatalf("new_session is on %q", got)
	}
	if _, ok := m.Action(ContextList, "n"); ok {
		t.Fatal("n still answers after the action moved off it")
	}
	// Everything it did not name is untouched.
	if got := m.Key(ContextList, NewGroup); got != "g" {
		t.Fatalf("an unrelated binding moved to %q", got)
	}
}

func TestOverridesRoundTripThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	want := Overrides{ContextList: {NewSession: {"z"}, Archive: {"delete", "x"}}}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := resolve(t, got)
	if key := m.Key(ContextList, NewSession); key != "z" {
		t.Fatalf("new_session came back as %q", key)
	}
	if keys := m.Keys(ContextList, Archive); len(keys) != 2 || keys[0] != "delete" {
		t.Fatalf("archive came back as %v", keys)
	}
}

// Only what differs is written, so a map saved today does not freeze every
// other binding at today's default.
func TestSavedFileHoldsOnlyTheChanges(t *testing.T) {
	m := resolve(t, Overrides{ContextList: {NewSession: {"z"}}})
	text := Encode(m.Overrides())
	if !strings.Contains(text, "new_session = [\"z\"]") {
		t.Fatalf("the change is missing:\n%s", text)
	}
	if strings.Contains(text, "new_group") {
		t.Fatalf("an unchanged binding was written out:\n%s", text)
	}
}

func TestMissingFileMeansDefaults(t *testing.T) {
	overrides, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("a missing key file is not an error: %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("a missing file yielded %v", overrides)
	}
}

func TestReferenceIsWrittenOnceAndThenLeftAlone(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReferenceIfMissing(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte("[list]\nnew_session = [\"z\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteReferenceIfMissing(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `new_session = ["z"]`) {
		t.Fatalf("the operator's file was overwritten:\n%s", raw)
	}
}

// The written reference is a file the loader accepts: every line is
// commented, so it resolves to the defaults rather than to an error.
func TestReferenceParsesAsAnEmptyOverride(t *testing.T) {
	overrides, err := Decode(Reference())
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 0 {
		t.Fatalf("the reference bound something: %v", overrides)
	}
}

func TestReservedKeysAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"the snippets chord", []string{"ctrl+alt+d"}},
		{"the § snippet", []string{"alt+§"}},
		{"the ± snippet", []string{"±"}},
		{"the interrupt", []string{"ctrl+c"}},
		{"a group number", []string{"4"}},
	} {
		m, problems := New(Overrides{ContextList: {NewSession: tc.keys}})
		if len(problems) == 0 {
			t.Errorf("%s was accepted", tc.name)
		}
		if got := m.Key(ContextList, NewSession); got != "n" {
			t.Errorf("%s: a refused override left new_session on %q", tc.name, got)
		}
	}
}

// Key files accept both the current Mac label and its legacy spelling.
func TestOptionIsAcceptedAsAltInTheKeyFile(t *testing.T) {
	m := resolve(t, Overrides{ContextFocus: {Editor: {"option+j"}}})
	if got := m.Key(ContextFocus, Editor); got != "alt+j" {
		t.Errorf("option+j bound as %q, want alt+j", got)
	}
	m = resolve(t, Overrides{ContextFocus: {Editor: {"⌥j"}}})
	if got := m.Key(ContextFocus, Editor); got != "alt+j" {
		t.Errorf("⌥j bound as %q, want alt+j", got)
	}
	if _, problems := New(Overrides{ContextList: {NewSession: {"ctrl+⌥d"}}}); len(problems) == 0 {
		t.Error("ctrl+⌥d was accepted into the snippets chord")
	}
	// Normalizing before the reserved check keeps the snippets chord owned
	// under either spelling.
	if _, problems := New(Overrides{ContextList: {NewSession: {"ctrl+option+d"}}}); len(problems) == 0 {
		t.Error("ctrl+option+d was accepted into the snippets chord")
	}
}

// An action a screen cannot be worked without may be moved but not removed.
func TestARequiredActionCannotBeUnbound(t *testing.T) {
	m, problems := New(Overrides{ContextRestore: {Cancel: {}}})
	if len(problems) == 0 {
		t.Fatal("unbinding the way out of the restore offer was accepted")
	}
	if !m.Bound(ContextRestore, Cancel) {
		t.Fatal("the restore offer has no way out of it")
	}
}

// A key file written for a screen this build no longer has still loads: the
// screen's bindings are reported and ignored, and everything else applies.
func TestARetiredScreenIsReportedAndIgnored(t *testing.T) {
	m, problems := New(Overrides{
		"retired":   {"old_action": {"r"}},
		ContextList: {"old_card": {"c"}, Archive: {"alt+a"}},
	})
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want the retired screen and the retired action", problems)
	}
	if !m.Bound(ContextList, Archive) {
		t.Fatal("a valid override beside retired ones was dropped")
	}
}

// An optional action may be unbound: that is how an operator gets a letter
// back for the agent.
func TestAnOptionalActionCanBeUnbound(t *testing.T) {
	m := resolve(t, Overrides{ContextFocus: {Archive: {}}})
	if m.Bound(ContextFocus, Archive) {
		t.Fatal("focus archive is still bound")
	}
	if cap := m.Cap(ContextFocus, Archive); cap != "" {
		t.Fatalf("an unbound action rendered as %q, which a footer would print", cap)
	}
}

// Two actions asking for one key is the case an operator hits by hand. The
// first in the catalog keeps it, the second is told, and nothing is lost
// without a word.
func TestACollisionIsReportedAndTheFirstKeeps(t *testing.T) {
	m, problems := New(Overrides{ContextList: {NewGroup: {"n"}}})
	if len(problems) == 0 {
		t.Fatal("two actions on n was accepted silently")
	}
	if action, _ := m.Action(ContextList, "n"); action != NewSession {
		t.Fatalf("n now answers %s", action)
	}
	if m.Bound(ContextList, NewGroup) {
		t.Fatal("new_group kept a key it lost the collision for")
	}
}

// A rebind from the screen takes the key: the operator pressed it
// deliberately, and the action that held it is reported rather than refused.
func TestRebindTakesTheKeyAndReportsWhoLostIt(t *testing.T) {
	m := resolve(t, nil)
	next, problems := m.Rebind(ContextList, NewGroup, []string{"n"})
	if action, _ := next.Action(ContextList, "n"); action != NewGroup {
		t.Fatalf("n answers %s after the rebind", action)
	}
	if next.Bound(ContextList, NewSession) {
		t.Fatal("new_session kept n")
	}
	found := false
	for _, problem := range problems {
		if problem.Action == NewSession {
			found = true
		}
	}
	if !found {
		t.Fatalf("nothing said new_session lost its key: %v", problems)
	}
	// And the map it was called on is untouched, so a refused rebind cannot
	// leave the board half-changed.
	if !m.Bound(ContextList, NewSession) {
		t.Fatal("the original map was mutated")
	}
}

func TestRebindOntoAReservedKeyIsRefused(t *testing.T) {
	m := resolve(t, nil)
	next, problems := m.Rebind(ContextList, NewGroup, []string{"ctrl+c"})
	if len(problems) == 0 {
		t.Fatal("ctrl+c was accepted")
	}
	if got := next.Key(ContextList, NewGroup); got != "g" {
		t.Fatalf("new_group moved to %q anyway", got)
	}
}

func TestResetPutsAnActionBack(t *testing.T) {
	m := resolve(t, Overrides{ContextList: {NewSession: {"z"}}})
	back, _ := m.Reset(ContextList, NewSession)
	if got := back.Key(ContextList, NewSession); got != "n" {
		t.Fatalf("reset left new_session on %q", got)
	}
	if len(back.Overrides()) != 0 {
		t.Fatalf("reset left overrides behind: %v", back.Overrides())
	}
}

// An unknown action is a typo in a hand-written file. It is reported, and
// everything else in the file still applies -- a key file is not all or
// nothing.
func TestAnUnknownActionIsReportedAndTheRestApplies(t *testing.T) {
	m, problems := New(Overrides{ContextList: {
		Action("teleport"): {"z"},
		NewGroup:           {"y"},
	}})
	if len(problems) != 1 {
		t.Fatalf("problems: %v", problems)
	}
	if got := m.Key(ContextList, NewGroup); got != "y" {
		t.Fatalf("the good line did not apply: %q", got)
	}
}

func TestDisplayRendersKeysForReading(t *testing.T) {
	for key, want := range map[string]string{
		"enter": "↵", "up": "↑", " ": "space",
		"ctrl+q": "ctrl+q", "x": "x",
	} {
		if got := Display(key); got != want {
			t.Errorf("Display(%q) = %q, want %q", key, got, want)
		}
	}
	if got := Compact("ctrl+n"); got != "^n" {
		t.Errorf("Compact(ctrl+n) = %q", got)
	}
	if got := Compact("ctrl+alt+d"); got != displayForOS("ctrl+alt+d", runtime.GOOS) {
		t.Errorf("Compact should leave the snippets chord alone, got %q", got)
	}
}

// A file the loader cannot parse must not take the board down with it.
func TestABrokenFileIsAnErrorNotAPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[list\nbroken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a broken file parsed")
	}
}

func TestDisplayUsesOSModifierNames(t *testing.T) {
	for _, tc := range []struct{ key, mac, other string }{
		{"alt+o", "⌥o", "alt+o"},
		{"alt+up", "⌥↑", "alt+↑"},
		{"alt+pgdown", "⌥pgdn", "alt+pgdn"},
		{"ctrl+alt+d", "ctrl+⌥d", "ctrl+alt+d"},
		{"alt+shift+up", "⌥shift+↑", "alt+shift+↑"},
		{"alt++", "⌥+", "alt++"},
		{"alt+", "⌥", "alt+"},
		{"ctrl+q", "ctrl+q", "ctrl+q"},
		{"alt", "alt", "alt"},
	} {
		for _, goos := range []string{"darwin", "linux", "windows"} {
			want := tc.other
			if goos == "darwin" {
				want = tc.mac
			}
			if got := displayForOS(tc.key, goos); got != want {
				t.Errorf("displayForOS(%q, %q) = %q, want %q", tc.key, goos, got, want)
			}
		}
	}
}

// A key file written before approve left the catalog still loads: the stale
// line is a warning that says where the job went, every other override still
// applies, and saving the map drops the line.
func TestRetiredApproveStillLoads(t *testing.T) {
	overrides, err := Decode("[list]\napprove = [\"±\"]\nnew_session = [\"alt+n\"]\n\n[focus]\napprove = [\"±\"]\n")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	m, problems := New(overrides)
	if len(problems) != 2 {
		t.Fatalf("got problems %v, want one per stale line", problems)
	}
	for _, problem := range problems {
		if problem.Action != "approve" || !strings.Contains(problem.Reason, "snippets.json") {
			t.Errorf("problem %q does not point at snippets.json", problem.Error())
		}
	}
	if got := m.Key(ContextList, NewSession); got != "alt+n" {
		t.Errorf("the stale line cost the override beside it: new_session on %q", got)
	}
	if strings.Contains(Encode(m.Overrides()), "approve") {
		t.Error("saving the map kept the retired action")
	}
}

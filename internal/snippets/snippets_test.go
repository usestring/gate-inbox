package snippets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWritesDefaultsOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != len(Defaults()) {
		t.Fatalf("got %d snippets, want the %d defaults", len(set.Snippets), len(Defaults()))
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("defaults were not written: %v", err)
	}
	var written []Snippet
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the file written on first run does not parse: %v", err)
	}
	// The written file is what the operator edits, so it has to round-trip
	// through the loader it will next be read by.
	if _, err := Load(dir); err != nil {
		t.Fatalf("reloading the file just written: %v", err)
	}
}

func TestLoadLeavesAnEditedFileAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[{"key":"d","label":"deploy","text":"ship it"}]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != 1 || set.Snippets[0].Text != "ship it" {
		t.Fatalf("got %+v, want only the edited entry", set.Snippets)
	}
	if set.Snippets[0].Binding() != "ctrl+alt+d" {
		t.Fatalf("binding %q, want ctrl+alt+d", set.Snippets[0].Binding())
	}
}

// A broken file must not silently fall back to the defaults: that would bind
// three keys the operator's own file had replaced, and hide the syntax error
// behind bindings that look deliberate.
func TestUnparseableFileYieldsNoSnippets(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[{"key":"d",}]`)
	set, err := Load(dir)
	if err == nil {
		t.Fatal("want an error for a file that does not parse")
	}
	if len(set.Snippets) != 0 {
		t.Fatalf("got %d snippets from a broken file, want none", len(set.Snippets))
	}
}

func TestRejectedEntriesAreReportedNotDropped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[
		{"key":"d","text":"ship it"},
		{"key":"1","text":"a digit"},
		{"key":"i","text":"unreachable"},
		{"key":"m","text":"also unreachable"},
		{"key":"","text":"no key"},
		{"key":"z","text":""},
		{"key":"d","text":"a repeat"}
	]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The one good entry survives: a typo must not cost the others.
	if len(set.Snippets) != 1 || set.Snippets[0].Key != "d" || set.Snippets[0].Text != "ship it" {
		t.Fatalf("got %+v, want only the first d entry", set.Snippets)
	}
	if len(set.Problems) != 6 {
		t.Fatalf("got %d problems, want 6: %v", len(set.Problems), set.Problems)
	}
	joined := strings.Join(set.Problems, "\n")
	for _, want := range []string{"single letter", "alt+tab", "alt+enter", "has no key", "sends nothing", "repeats"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems do not explain %q:\n%s", want, joined)
		}
	}
}

func TestGetMatchesOnlyTheFullChord(t *testing.T) {
	set := validate([]Snippet{{Key: "d", Text: "ship it"}})
	if _, ok := set.Get("ctrl+alt+d"); !ok {
		t.Fatal("ctrl+alt+d should match")
	}
	// The bare letter is what a focused agent is being typed at.
	for _, key := range []string{"d", "alt+d", "ctrl+d"} {
		if _, ok := set.Get(key); ok {
			t.Errorf("%q must not match a snippet: it belongs to the pane", key)
		}
	}
}

func TestSnippetsAreOrderedByKey(t *testing.T) {
	set := validate([]Snippet{{Key: "z", Text: "z"}, {Key: "a", Text: "a"}, {Key: "n", Text: "n"}})
	var keys []string
	for _, snip := range set.Snippets {
		keys = append(keys, snip.Key)
	}
	if got := strings.Join(keys, ""); got != "anz" {
		t.Fatalf("order %q, want anz — every surface lists them the same way", got)
	}
}

func TestTitleFallsBackToTheText(t *testing.T) {
	if got := (Snippet{Text: "ship it"}).Title(); got != "ship it" {
		t.Fatalf("Title() = %q, want the text when there is no label", got)
	}
	if got := (Snippet{Label: "deploy", Text: "ship it"}).Title(); got != "deploy" {
		t.Fatalf("Title() = %q, want the label", got)
	}
}

// Multi-line snippets keep their shape: an operator who wrote a message across
// several lines meant those lines, and only the edges are trimmed.
func TestTextKeepsItsInteriorNewlines(t *testing.T) {
	set := validate([]Snippet{{Key: "d", Text: "\n first\nsecond \n"}})
	if len(set.Snippets) != 1 {
		t.Fatalf("got %+v, want one snippet", set.Snippets)
	}
	if got := set.Snippets[0].Text; got != "first\nsecond" {
		t.Fatalf("Text = %q, want the interior newline kept and the edges trimmed", got)
	}
}

func TestDefaultsAllBind(t *testing.T) {
	set := validate(Defaults())
	if len(set.Problems) != 0 {
		t.Fatalf("the defaults must all bind, got: %v", set.Problems)
	}
	if len(set.Snippets) != len(Defaults()) {
		t.Fatalf("got %d of %d defaults", len(set.Snippets), len(Defaults()))
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "snippets.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSectionKeyBindsOnAltAlone(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "summarise"}, {Key: "d", Text: "ship it"}})
	if len(set.Snippets) != 2 {
		t.Fatalf("got %+v, want both entries to bind: %v", set.Snippets, set.Problems)
	}
	got := map[string]string{}
	for _, snip := range set.Snippets {
		got[snip.Key] = snip.Binding()
	}
	if got[SectionKey] != "alt+"+SectionKey {
		t.Errorf("%s binds %q, want alt+%s — ctrl cannot carry it", SectionKey, got[SectionKey], SectionKey)
	}
	// The exception must not have moved the chord underneath the letters.
	if got["d"] != "ctrl+alt+d" {
		t.Errorf("d binds %q, want ctrl+alt+d", got["d"])
	}
}

// A repeated § is reported as the key the operator actually presses.
func TestRepeatedSectionKeyNamesItsRealBinding(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "a"}, {Key: SectionKey, Text: "b"}})
	if len(set.Problems) != 1 {
		t.Fatalf("got problems %v, want the repeat reported once", set.Problems)
	}
	if !strings.Contains(set.Problems[0], "repeats alt+"+SectionKey+",") {
		t.Errorf("the repeat names the wrong key: %q", set.Problems[0])
	}
}

// § and ± are two exceptions, not a general widening: every other key outside
// a-z is still a Problem.
func TestSectionKeyDoesNotLoosenTheKeyRule(t *testing.T) {
	for _, key := range []string{"¶", "é", "1", ",", "ss", "alt+±"} {
		set := validate([]Snippet{{Key: key, Text: "x"}})
		if len(set.Snippets) != 0 {
			t.Errorf("key %q bound, want it refused", key)
		}
		if len(set.Problems) != 1 {
			t.Errorf("key %q produced %d problems, want 1", key, len(set.Problems))
		}
	}
}

func TestGetMatchesTheSectionKeyOnlyOnAlt(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "summarise"}})
	if _, ok := set.Get("alt+" + SectionKey); !ok {
		t.Fatalf("alt+%s should match", SectionKey)
	}
	// A bare § hands the session over and ctrl+§ never arrives; neither may
	// reach the snippet.
	for _, key := range []string{SectionKey, "ctrl+" + SectionKey, "ctrl+alt+" + SectionKey} {
		if _, ok := set.Get(key); ok {
			t.Errorf("%q must not match the § snippet", key)
		}
	}
}

// ± binds as itself, with no modifier: it is the one bare snippet key.
func TestPlusMinusBindsBare(t *testing.T) {
	set := validate([]Snippet{{Key: PlusMinusKey, Text: "merge it"}})
	if len(set.Problems) != 0 {
		t.Fatalf("± refused: %v", set.Problems)
	}
	snip, ok := set.Get(PlusMinusKey)
	if !ok {
		t.Fatal("a bare ± should match")
	}
	if !snip.Bare() {
		t.Error("± does not report itself bare, so a text input would swallow the character")
	}
	for _, key := range []string{"alt+" + PlusMinusKey, "ctrl+alt+" + PlusMinusKey} {
		if _, ok := set.Get(key); ok {
			t.Errorf("%q must not match the ± snippet", key)
		}
	}
}

func TestIsBindingAdmitsEveryForm(t *testing.T) {
	for _, key := range []string{"ctrl+alt+c", "alt+" + SectionKey, PlusMinusKey} {
		if !IsBinding(key) {
			t.Errorf("IsBinding(%q) = false, want true", key)
		}
	}
	// Everything else is the pane's, and must be settled without walking the set.
	for _, key := range []string{"c", "alt+c", "ctrl+c", SectionKey, "alt+w"} {
		if IsBinding(key) {
			t.Errorf("IsBinding(%q) = true, want false — that key belongs to the pane", key)
		}
	}
}

// The default on § is the whole reason the alt binding exists, so it is pinned
// by what it has to ask for rather than by its exact wording.
func TestDefaultsCarryTheProgressSummaryOnTheSectionKey(t *testing.T) {
	var found *Snippet
	for _, snip := range Defaults() {
		if snip.Key == SectionKey {
			found = &snip
		}
	}
	if found == nil {
		t.Fatal("no default on §")
	}
	for _, want := range []string{"bullet points", "PR link", "status", "next steps", "finished"} {
		if !strings.Contains(found.Text, want) {
			t.Errorf("the § default does not ask for %q: %q", want, found.Text)
		}
	}
	// It prints in the agent's pane. A key that could send something outward
	// would be one the operator had to think before pressing.
	for _, never := range []string{"slack", "email", "post ", "send "} {
		if strings.Contains(strings.ToLower(found.Text), never) {
			t.Errorf("the § default asks the agent to %q, but it must only answer on the pane: %q", never, found.Text)
		}
	}
}

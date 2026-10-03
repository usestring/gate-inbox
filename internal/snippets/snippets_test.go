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
	if set.Snippets[0].Binding() != "d" {
		t.Fatalf("binding %q, want d", set.Snippets[0].Binding())
	}
}

// A snippet's chord is named in the file, not derived from its key; § and ±
// can carry none. The set resolves a spelled chord back to its snippet, Mac
// and alt spellings alike.
func TestChordIsNamedInTheFile(t *testing.T) {
	set := Set{Snippets: []Snippet{
		{Key: "d", Chord: "option+shift+d", Text: "ship it"},
		{Key: PlusMinusKey, Text: "approve"},
		{Key: SectionKey, Text: "progress"},
	}}
	if got := set.Snippets[0].Chord; got != "option+shift+d" {
		t.Fatalf("d chord = %q, want option+shift+d", got)
	}
	for _, snip := range set.Snippets[1:] {
		if snip.Chord != "" {
			t.Errorf("%s chord = %q, want none", snip.Key, snip.Chord)
		}
	}
	for _, spelling := range []string{"option+shift+d", "alt+shift+d", "alt+shift+D", "⌥shift+d"} {
		if snip, ok := set.Chord(spelling); !ok || snip.Key != "d" {
			t.Fatalf("set did not resolve %q to d: %v %v", spelling, snip, ok)
		}
	}
	if _, ok := set.Chord("alt+shift+x"); ok {
		t.Fatal("set resolved a chord no snippet carries")
	}
}

// A chord must hold a modifier and be unique; a bare or repeated one is a
// problem, not a silent no-op. The Mac and alt spellings of one chord collide.
func TestAChordMustBeAUniqueModifiedChord(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[
	  {"key":"d","chord":"d","text":"ship it"},
	  {"key":"e","chord":"alt+shift+d","text":"one"},
	  {"key":"f","chord":"option+shift+d","text":"two"}
	]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != 1 || set.Snippets[0].Key != "e" {
		t.Fatalf("bound %+v, want only e", set.Snippets)
	}
	if len(set.Problems) != 2 {
		t.Fatalf("problems = %q, want two", set.Problems)
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
	if len(set.Problems) != 4 {
		t.Fatalf("got %d problems, want 4: %v", len(set.Problems), set.Problems)
	}
	joined := strings.Join(set.Problems, "\n")
	for _, want := range []string{"single letter", "has no key", "sends nothing", "repeats"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems do not explain %q:\n%s", want, joined)
		}
	}
}

func TestGetMatchesOnlyTheBareKey(t *testing.T) {
	set := validate([]Snippet{{Key: "d", Text: "ship it"}})
	if _, ok := set.Get("d"); !ok {
		t.Fatal("d should match")
	}
	// No chord exists anymore: a modifier on the key is a different key.
	for _, key := range []string{"alt+d", "ctrl+d", "ctrl+alt+d"} {
		if _, ok := set.Get(key); ok {
			t.Errorf("%q must not match a snippet: the menu reads bare keys", key)
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

func TestSectionKeyBindsBare(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "summarise"}, {Key: "d", Text: "ship it"}})
	if len(set.Snippets) != 2 {
		t.Fatalf("got %+v, want both entries to bind: %v", set.Snippets, set.Problems)
	}
	got := map[string]string{}
	for _, snip := range set.Snippets {
		got[snip.Key] = snip.Binding()
	}
	if got[SectionKey] != SectionKey {
		t.Errorf("%s binds %q, want a bare %s — the menu reads bare keys", SectionKey, got[SectionKey], SectionKey)
	}
	if got["d"] != "d" {
		t.Errorf("d binds %q, want d", got["d"])
	}
}

// A repeated § is reported as the key the operator actually presses.
func TestRepeatedSectionKeyNamesItsRealBinding(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "a"}, {Key: SectionKey, Text: "b"}})
	if len(set.Problems) != 1 {
		t.Fatalf("got problems %v, want the repeat reported once", set.Problems)
	}
	if !strings.Contains(set.Problems[0], "repeats "+SectionKey+",") {
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

func TestGetMatchesTheSectionKeyBare(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "summarise"}})
	if _, ok := set.Get(SectionKey); !ok {
		t.Fatalf("%s should match", SectionKey)
	}
	// A modifier on § is a different key: the menu reads bare keys.
	for _, key := range []string{"alt+" + SectionKey, "ctrl+" + SectionKey} {
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

func TestIsBindingAdmitsOnlyTheBareKey(t *testing.T) {
	if !IsBinding(PlusMinusKey) {
		t.Errorf("IsBinding(%q) = false, want true — it is the one key that fires outside the menu", PlusMinusKey)
	}
	// Everything else answers in the menu, never where the operator types.
	for _, key := range []string{"c", "alt+c", "ctrl+c", "ctrl+alt+c", SectionKey, "alt+" + SectionKey} {
		if IsBinding(key) {
			t.Errorf("IsBinding(%q) = true, want false — that key belongs to the pane or the menu", key)
		}
	}
}

// The default on § is pinned by what it has to ask for rather than by its
// exact wording.
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

// Every entry the board writes spells autoSubmit out, so the operator sees the
// switch in the file they edit rather than having to know it exists.
func TestDefaultsSpellOutAutoSubmit(t *testing.T) {
	for _, snip := range Defaults() {
		if snip.AutoSubmit == nil || !*snip.AutoSubmit {
			t.Errorf("default %q does not set autoSubmit true", snip.Title())
		}
	}
	dir := t.TempDir()
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(raw), `"autoSubmit": true`), len(Defaults()); got != want {
		t.Fatalf("first-run file has %d autoSubmit fields, want %d:\n%s", got, want, raw)
	}
}

func TestDefaultsCarryQuickReplies(t *testing.T) {
	texts := map[string]bool{}
	for _, snip := range Defaults() {
		texts[snip.Text] = true
	}
	for _, want := range []string{
		"yes",
		"continue",
		"I'm confused, explain like I'm 5",
	} {
		if !texts[want] {
			t.Errorf("defaults have no %q", want)
		}
	}
}

// An entry written before the field existed still submits; false is kept.
func TestAutoSubmitDefaultsToYes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[{"key":"d","text":"ship it"},{"key":"f","text":"draft","autoSubmit":false},{"key":"g","text":"go","autoSubmit":true}]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]bool{"d": true, "f": false, "g": true}
	for _, snip := range set.Snippets {
		if snip.Submits() != want[snip.Key] {
			t.Errorf("%s submits %v, want %v", snip.Key, snip.Submits(), want[snip.Key])
		}
	}
}

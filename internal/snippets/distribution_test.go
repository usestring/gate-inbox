package snippets

import (
	"os"
	"strings"
	"testing"
)

func useDistribution(t *testing.T, entries ...Snippet) {
	t.Helper()
	restore, err := UseDistribution(entries)
	if err != nil {
		t.Fatalf("UseDistribution: %v", err)
	}
	t.Cleanup(restore)
}

var sample = Snippet{Key: "r", Label: "review the diff", Text: "review the diff for mistakes"}

func TestDistributionEntryBindsBesideTheOperatorsFile(t *testing.T) {
	useDistribution(t, sample)
	dir := t.TempDir()
	write(t, dir, `[{"key":"d","label":"deploy","text":"ship it"}]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != 2 || len(set.Problems) != 0 {
		t.Fatalf("got %+v, want the operator's entry and the distribution's", set)
	}
	if got, ok := set.Get("ctrl+alt+r"); !ok || got != sample {
		t.Fatalf("ctrl+alt+r = %+v, %v; want the distribution's entry", got, ok)
	}
	if got, _ := set.Get("ctrl+alt+d"); got.Text != "ship it" {
		t.Fatalf("ctrl+alt+d = %+v, want the operator's entry", got)
	}
}

func TestOperatorEntryWinsAKeyClash(t *testing.T) {
	useDistribution(t, sample)
	dir := t.TempDir()
	write(t, dir, `[{"key":" R ","label":"mine","text":"my own words"}]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != 1 || set.Snippets[0].Text != "my own words" || len(set.Problems) != 0 {
		t.Fatalf("got %+v, want only the operator's entry on r", set)
	}
}

// An operator entry that cannot bind still claims its key: it is theirs, and
// the problem it raises is why the key does nothing, not a cue to fall back.
func TestOperatorEntryThatCannotBindStillClaimsItsKey(t *testing.T) {
	useDistribution(t, sample)
	dir := t.TempDir()
	write(t, dir, `[{"key":"r","text":""}]`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := set.Get("ctrl+alt+r"); ok || len(set.Problems) != 1 {
		t.Fatalf("got %+v, want r unbound with one problem", set)
	}
}

func TestFirstRunDoesNotWriteTheDistribution(t *testing.T) {
	override := Snippet{Key: "c", Label: "carry on", Text: "carry on with the plan"}
	useDistribution(t, sample, override)
	dir := t.TempDir()
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := set.Get("ctrl+alt+c"); got != override {
		t.Fatalf("ctrl+alt+c = %+v, want the distribution's entry over the built-in", got)
	}
	if got, _ := set.Get("ctrl+alt+r"); got != sample {
		t.Fatalf("ctrl+alt+r = %+v, want the distribution's entry", got)
	}
	if len(set.Snippets) != len(Defaults())+1 {
		t.Fatalf("got %d snippets, want the built-ins with c replaced plus r", len(set.Snippets))
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	written := string(raw)
	for _, leaked := range []string{sample.Text, override.Text, `"c"`} {
		if strings.Contains(written, leaked) {
			t.Fatalf("snippets.json carries %s:\n%s", leaked, written)
		}
	}
	if !strings.Contains(written, `"t"`) {
		t.Fatalf("snippets.json lost the built-ins the distribution does not supply:\n%s", written)
	}

	// A later load reads that file and merges the distribution again, and
	// still writes nothing.
	again, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(again.Snippets) != len(set.Snippets) {
		t.Fatalf("reload bound %d snippets, want %d", len(again.Snippets), len(set.Snippets))
	}
	if after, _ := os.ReadFile(Path(dir)); string(after) != written {
		t.Fatalf("a reload rewrote snippets.json")
	}
}

// With the distribution changed, a key the operator never bound follows it.
func TestADefaultTheOperatorNeverBoundFollowsTheDistribution(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `[{"key":"d","text":"ship it"}]`)
	useDistribution(t, sample)
	changed := sample
	changed.Text = "review the diff again"
	useDistribution(t, changed)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := set.Get("ctrl+alt+r"); got.Text != changed.Text {
		t.Fatalf("ctrl+alt+r = %+v, want the distribution's current text", got)
	}
}

func TestEmptyDistributionIsANoOp(t *testing.T) {
	useDistribution(t)
	dir := t.TempDir()
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Snippets) != len(Defaults()) {
		t.Fatalf("got %d snippets, want the %d built-ins", len(set.Snippets), len(Defaults()))
	}
	write(t, dir, `[{"key":"d","text":"ship it"}]`)
	set, err = Load(dir)
	if err != nil || len(set.Snippets) != 1 {
		t.Fatalf("got %+v, %v; want the file alone", set, err)
	}
}

func TestUseDistributionRefusesAnEntryThatCannotBind(t *testing.T) {
	for name, entries := range map[string][]Snippet{
		"digit":     {{Key: "1", Text: "x"}},
		"no text":   {{Key: "r"}},
		"unreached": {{Key: "i", Text: "x"}},
		"repeat":    {sample, sample},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := UseDistribution(entries); err == nil || !strings.Contains(err.Error(), "snippet defaults") {
				t.Fatalf("UseDistribution = %v, want a refusal", err)
			}
			if currentDistribution() != nil {
				t.Fatal("a refused set was installed")
			}
		})
	}
}

func TestUseDistributionRestores(t *testing.T) {
	restore, err := UseDistribution([]Snippet{sample})
	if err != nil {
		t.Fatal(err)
	}
	if len(currentDistribution()) != 1 {
		t.Fatal("not installed")
	}
	restore()
	if currentDistribution() != nil {
		t.Fatal("restore left the entries installed")
	}
}

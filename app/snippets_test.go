package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/snippets"
)

// clearSnippetDefaults undoes what a Run in this test installed.
func clearSnippetDefaults(t *testing.T) {
	t.Cleanup(func() {
		if _, err := snippets.UseDistribution(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunLaysSnippetDefaultsUnderTheOperatorsFile(t *testing.T) {
	clearSnippetDefaults(t)
	t.Setenv(config.HomeEnv, t.TempDir())
	supplied := Snippet{Key: "r", Label: "review the diff", Text: "review the diff for mistakes"}
	if err := Run(context.Background(), []string{"--version"}, Options{SnippetDefaults: []Snippet{supplied}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(snippets.Path(dir), []byte(`[{"key":"d","text":"ship it"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := snippets.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := set.Get("ctrl+alt+r")
	if !ok || got.Text != supplied.Text || got.Label != supplied.Label {
		t.Fatalf("ctrl+alt+r = %+v, %v; want the build's snippet", got, ok)
	}
	if _, ok := set.Get("ctrl+alt+d"); !ok {
		t.Fatal("the operator's own entry did not bind")
	}
	written, _ := os.ReadFile(snippets.Path(dir))
	if strings.Contains(string(written), supplied.Text) {
		t.Fatal("the build's snippet was written into the operator's file")
	}
}

// A later Run with none clears what an earlier one installed, so the
// defaults belong to the build that passed them.
func TestRunWithNoSnippetDefaultsClearsThem(t *testing.T) {
	clearSnippetDefaults(t)
	t.Setenv(config.HomeEnv, t.TempDir())
	opts := Options{SnippetDefaults: []Snippet{{Key: "r", Text: "review the diff"}}}
	if err := Run(context.Background(), []string{"--version"}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := Run(context.Background(), []string{"--version"}, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(snippets.Path(dir), []byte(`[{"key":"d","text":"ship it"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := snippets.Load(dir)
	if err != nil || len(set.Snippets) != 1 {
		t.Fatalf("got %+v, %v; want the operator's file alone", set, err)
	}
}

func TestRunRefusesASnippetDefaultThatCannotBind(t *testing.T) {
	clearSnippetDefaults(t)
	t.Setenv(config.HomeEnv, t.TempDir())
	err := Run(context.Background(), []string{"--version"}, Options{SnippetDefaults: []Snippet{{Key: "r"}}})
	if err == nil || !strings.Contains(err.Error(), "sends nothing") {
		t.Fatalf("Run = %v, want the empty entry refused", err)
	}
}

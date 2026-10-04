package promptname

import (
	"context"
	"strings"
	"testing"
)

func TestNameSendsTheTruncatedPromptAndReadsTheLastLine(t *testing.T) {
	var sent string
	n := &Namer{command: []string{"x"}, run: func(_ context.Context, _ []string, stdin string) (string, error) {
		sent = stdin
		return "thinking...\n`advance-all-goals`\n", nil
	}}
	got, err := n.Name(context.Background(), strings.Repeat("a", MaxPromptChars+50))
	if err != nil {
		t.Fatal(err)
	}
	if got != "advance-all-goals" {
		t.Fatalf("Name = %q", got)
	}
	if !strings.HasSuffix(sent, strings.Repeat("a", MaxPromptChars)) || strings.HasSuffix(sent, strings.Repeat("a", MaxPromptChars+1)) {
		t.Fatalf("prompt was not cut to %d chars", MaxPromptChars)
	}
}

func TestNameRefusesAnEmptyPrompt(t *testing.T) {
	n := &Namer{run: func(context.Context, []string, string) (string, error) {
		t.Fatal("model called for an empty prompt")
		return "", nil
	}}
	if _, err := n.Name(context.Background(), "   "); err == nil {
		t.Fatal("want an error")
	}
}

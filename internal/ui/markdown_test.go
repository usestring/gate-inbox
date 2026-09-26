package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
)

const markdownSample = "# Plan\n\nRun **go test** with `-short`, see [the PR](https://github.com/o/r/pull/1).\n\n- first *item* here\n  - nested\n1. numbered\n\n> quoted note\n\n```go\nfunc main() {\n    fmt.Println(\"hi\")\n}\n```\n---"

func TestMarkdownLinesDropSyntaxAndKeepText(t *testing.T) {
	rows := markdownLines(markdownSample, 80)
	plain := ansi.Strip(strings.Join(rows, "\n"))
	for _, want := range []string{"Plan", "Run go test with -short, see the PR (https://github.com/o/r/pull/1).", "• first item here", "  • nested", "1. numbered", "│ quoted note", "func main() {", "    fmt.Println", "────"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in:\n%s", want, plain)
		}
	}
	for _, gone := range []string{"# Plan", "**", "`", "](", "> quoted"} {
		if strings.Contains(plain, gone) {
			t.Fatalf("markdown syntax %q survived:\n%s", gone, plain)
		}
	}
}

func TestMarkdownLinesStyleEachRowOnItsOwn(t *testing.T) {
	text := "plain **bold words that run across several wrapped rows of text** and `code that also wraps across rows`"
	for _, width := range []int{1, 7, 12, 30} {
		rows := markdownLines(text, width)
		for _, row := range rows {
			if textfmt.Width(row) > width {
				t.Fatalf("width %d: row is %d cells: %q", width, textfmt.Width(row), row)
			}
			if strings.Contains(row, "\x1b[") && !strings.HasSuffix(row, "m") {
				t.Fatalf("width %d: row leaves a style open: %q", width, row)
			}
		}
		joined := strings.Join(strings.Fields(ansi.Strip(strings.Join(rows, " "))), "")
		if want := strings.Join(strings.Fields("plain bold words that run across several wrapped rows of text and code that also wraps across rows"), ""); joined != want {
			t.Fatalf("width %d: text changed: %q", width, joined)
		}
	}
	if rows := markdownLines(text, 30); rows[0] == ansi.Strip(rows[0]) {
		t.Fatal("rendered rows carry no styling")
	}
}

func TestMarkdownEmphasisNeedsAPair(t *testing.T) {
	for _, text := range []string{"2 * 3 = 6", "glob *.go", "a ** b", "unclosed `tick"} {
		if plain := ansi.Strip(strings.Join(markdownLines(text, 40), "")); plain != text {
			t.Fatalf("%q rendered as %q", text, plain)
		}
	}
}

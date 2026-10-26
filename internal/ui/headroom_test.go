package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHeadroomLinesKeepQuotaWithinRail(t *testing.T) {
	const quota = "5h 80% · week 65%"
	for _, name := range []string{"codex", "long-custom-tool-name", "工具工具工具工具工具"} {
		t.Run(name, func(t *testing.T) {
			const width = 36
			m := Model{headroom: map[string]string{name: quota}}
			lines := m.headroomLines(width)
			if len(lines) != 2 {
				t.Fatalf("got %d lines, want heading and one quota row", len(lines))
			}
			row := ansi.Strip(lines[1])
			if got := ansi.StringWidth(row); got > width {
				t.Errorf("row width = %d, want at most %d: %q", got, width, row)
			}
			if !strings.HasSuffix(row, quota) {
				t.Errorf("quota missing from row: %q", row)
			}
			if prefix := strings.TrimSuffix(row, quota); ansi.StringWidth(prefix) != railInset+9 {
				t.Errorf("quota starts at column %d, want %d", ansi.StringWidth(prefix), railInset+9)
			}
		})
	}
}

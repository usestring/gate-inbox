package claudeplugin

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGISweepCommandShipsAndOnlyProposes(t *testing.T) {
	dir, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := string(readFile(t, filepath.Join(dir, "commands/gi-sweep.md")))
	for _, want := range []string{"description:", `"$GATE_INBOX_BIN" sweep --json $ARGUMENTS`, "Do not call archive_session", "only proposes"} {
		if !strings.Contains(text, want) {
			t.Errorf("gi-sweep.md lacks %q", want)
		}
	}
}

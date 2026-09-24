package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Slog logger made before the log is open writes into it once it is,
// scrubbed and levelled like the package's own lines, carrying its With
// attributes and groups.
func TestSlogWritesThroughTheDefaultOpenedLater(t *testing.T) {
	early := Slog().With("extension", "demo").WithGroup("grp")

	path := filepath.Join(t.TempDir(), "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelInfo, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	restore := SetDefault(logger)
	defer SetDefault(restore)

	early.Debug("below the level")
	early.Info("ran", "note", "key "+secretFixtures[0].secret)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "msg=ran extension=demo grp.note=") {
		t.Fatalf("want the line with its attributes and group:\n%s", body)
	}
	if strings.Contains(body, "below the level") {
		t.Fatalf("a debug line reached an info log:\n%s", body)
	}
	if strings.Contains(body, secretFixtures[0].secret) {
		t.Fatalf("a token reached the log:\n%s", body)
	}
}

func TestSlogWithNoLogOpenWritesNothing(t *testing.T) {
	restore := SetDefault(nil)
	defer SetDefault(restore)
	if Slog().Enabled(t.Context(), LevelError) {
		t.Fatal("the disabled default took a line")
	}
	Slog().Error("nowhere")
}

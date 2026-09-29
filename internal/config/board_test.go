package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sidebar side is read from [board], defaults to the right when the
// file says nothing, and takes either spelling of case and padding.
func TestBoardSidebar(t *testing.T) {
	for _, tc := range []struct {
		name, file, want string
	}{
		{name: "absent file", file: "", want: SidebarRight},
		{name: "no board section", file: "poll_interval = \"2s\"\n", want: SidebarRight},
		{name: "empty value", file: "[board]\nsidebar = \"\"\n", want: SidebarRight},
		{name: "right", file: "[board]\nsidebar = \"right\"\n", want: SidebarRight},
		{name: "left", file: "[board]\nsidebar = \"left\"\n", want: SidebarLeft},
		{name: "loose spelling", file: "[board]\nsidebar = \" Left \"\n", want: SidebarLeft},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadFile(t, tc.file).Board.Sidebar; got != tc.want {
				t.Fatalf("board.sidebar = %q, want %q", got, tc.want)
			}
		})
	}
}

// A side the board cannot draw is refused at load, naming the key, rather
// than quietly drawn as the default.
func TestBoardSidebarRejectsUnknownSide(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[board]\nsidebar = \"top\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "board.sidebar") {
		t.Fatalf("LoadDir err = %v, want one naming board.sidebar", err)
	}
}

// The written default documents the setting, commented out, and the
// built-in config resolves it to the right.
func TestDefaultConfigDocumentsSidebar(t *testing.T) {
	if !strings.Contains(defaultConfig, "# [board]\n# sidebar = \"right\"\n") {
		t.Fatal("default config does not document [board] sidebar")
	}
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Board.Sidebar != SidebarRight {
		t.Fatalf("default board.sidebar = %q, want %q", cfg.Board.Sidebar, SidebarRight)
	}
}

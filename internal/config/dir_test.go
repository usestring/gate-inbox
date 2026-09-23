package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirHonoursHomeThenUserConfigDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os.UserConfigDir reads XDG_CONFIG_HOME only on linux")
	}
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	t.Setenv(HomeEnv, "/elsewhere")
	if got, err := Dir(); err != nil || got != "/elsewhere" {
		t.Fatalf("Dir with %s = %q, %v", HomeEnv, got, err)
	}

	t.Setenv(HomeEnv, "")
	if got, err := Dir(); err != nil || got != filepath.Join(base, DirName) {
		t.Fatalf("Dir without %s = %q, %v", HomeEnv, got, err)
	}
}

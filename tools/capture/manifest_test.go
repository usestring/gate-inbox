package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type manifest struct {
	Version       int      `json:"version"`
	Producer      string   `json:"producer"`
	RunID         string   `json:"run_id"`
	Fixture       bool     `json:"fixture"`
	TmuxTmpdir    string   `json:"tmux_tmpdir"`
	GateInboxHome string   `json:"gate_inbox_home"`
	Home          string   `json:"home"`
	Files         []string `json:"files"`
}

func TestManifestRecordsScratchStateAndEveryFile(t *testing.T) {
	dir := t.TempDir()
	still := filepath.Join(dir, "zz-final.png")
	gif := filepath.Join(dir, "board.gif")
	os.WriteFile(still, []byte("png"), 0o644)
	os.WriteFile(gif, []byte("gif"), 0o644)
	out := filepath.Join(dir, "capture.json")

	cmd := exec.Command("bash", "manifest.sh", out, "20261012-120000", "/tmp/gib-cap.abc", dir+`/wo"rk`, "/tmp/gib-cap.abc/home", still, gif)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("manifest.sh: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	want := manifest{1, "gate-inbox/tools/capture", "20261012-120000", true, "/tmp/gib-cap.abc", dir + `/wo"rk`, "/tmp/gib-cap.abc/home", []string{sum("png"), sum("gif")}}
	if m.Version != want.Version || m.Producer != want.Producer || m.RunID != want.RunID || !m.Fixture ||
		m.TmuxTmpdir != want.TmuxTmpdir || m.GateInboxHome != want.GateInboxHome || m.Home != want.Home ||
		len(m.Files) != 2 || m.Files[0] != want.Files[0] || m.Files[1] != want.Files[1] {
		t.Fatalf("manifest = %+v, want %+v", m, want)
	}
}

func TestManifestRefusesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "capture.json")
	cmd := exec.Command("bash", "manifest.sh", out, "run", "/tmp/a", "/tmp/b", "/tmp/c", filepath.Join(dir, "absent.png"))
	if err := cmd.Run(); err == nil {
		t.Fatal("manifest.sh accepted a file that does not exist")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("manifest.sh left a manifest behind after failing")
	}
}

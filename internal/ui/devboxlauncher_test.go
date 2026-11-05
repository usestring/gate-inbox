package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestDevboxLauncherMapsAndForwardsRequest(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!"+python+"\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write("ssh", `import os, subprocess, sys
if "fixture-host" not in sys.argv: sys.exit("unexpected SSH target")
if "attach-session" in sys.argv[-1]:
    with open(os.environ["ATTACH_RECEIPT"], "w") as stream: stream.write(sys.argv[-1])
    sys.exit(0)
sys.exit(subprocess.call(["sh", "-c", sys.argv[-1]]))
`)
	receipt := filepath.Join(bin, "received.json")
	attachReceipt := filepath.Join(bin, "attached.txt")
	write("gate-inbox", `import json, os, sys
with open(os.environ["LAUNCH_RECEIPT"], "w") as stream: json.dump({"argv":sys.argv[1:],"socket":os.environ.get("GATE_INBOX_TMUX_SOCKET","")},stream)
print(json.dumps({"id":"fixture-1"}))
`)
	local := t.TempDir()
	remote := t.TempDir()
	subdir := "nested project"
	if err := os.Mkdir(filepath.Join(local, subdir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(remote, subdir), 0o700); err != nil {
		t.Fatal(err)
	}
	prompt := "keep this literal: $(touch should-not-exist); 'quoted' café"
	request := filepath.Join(bin, "request.json")
	body, err := json.Marshal(manualLaunchRequest{Tool: "codex", Model: "chosen-model", Name: "new-task", Prompt: prompt, Directory: filepath.Join(local, subdir)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request, body, 0o600); err != nil {
		t.Fatal(err)
	}
	launcher, err := filepath.Abs("../../scripts/devbox-launch.py")
	if err != nil {
		t.Fatal(err)
	}
	run := func(mapping string) error {
		t.Helper()
		cmd := exec.Command(python, launcher, "--host", "fixture-host", "--socket", "private-board", "--map", mapping, request)
		cmd.Dir = bin
		cmd.Env = append(tmuxtest.Environ(), "PATH="+bin+string(os.PathListSeparator)+"/usr/bin:/bin", "HOME="+bin, "XDG_CONFIG_HOME="+bin, "LAUNCH_RECEIPT="+receipt, "ATTACH_RECEIPT="+attachReceipt)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}
	if err := run(local + "=" + remote); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var received struct {
		Argv   []string `json:"argv"`
		Socket string   `json:"socket"`
	}
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatal(err)
	}
	argv := received.Argv
	if received.Socket != "private-board" {
		t.Fatalf("spawn ran on socket %q, want the --socket server", received.Socket)
	}
	attached, err := os.ReadFile(attachReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attached), "-L private-board attach-session") {
		t.Fatalf("attach did not target the --socket server: %q", attached)
	}
	for key, want := range map[string]string{"--tool": "codex", "--model": "chosen-model", "--directory": filepath.Join(remote, subdir), "--prompt": prompt} {
		at := slices.Index(argv, key)
		if at < 0 || at+1 >= len(argv) || argv[at+1] != want {
			t.Fatalf("%s not preserved: %q", key, argv)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("prompt executed as shell code")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := run(filepath.Join(local, "different") + "=" + remote); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("unmapped directory accepted: %v", err)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("unmapped request launched")
	}
}

package remotehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func TestSSHQuotesWorkerPathsAndSessionID(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gate inbox's")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
printf '%s
' "$GATE_INBOX_HOME" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	host := config.Host{SSH: "box", Binary: bin, Home: "/tmp/board $(exit 91)"}
	id := "sess'; exit 92; #"
	args := arguments(host, "-tt", "attach", id)
	if tail := args[len(args)-3 : len(args)-1]; !slices.Equal(tail, []string{"--", "box"}) {
		t.Fatalf("SSH destination argv = %q", tail)
	}
	// Use a POSIX shell with a stand-in exec to observe exactly what SSH's
	// worker shell would pass on, without connecting to a host.
	cmd := exec.Command("sh", "-c", `exec() { printf '%s\n' "$@"; }
`+args[len(args)-1])
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("parse worker command: %v: %s", err, out)
	}
	want := []string{host.Home, "host", "attach", id}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("worker argv = %q, want %q", got, want)
	}
}

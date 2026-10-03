package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/cli"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// hookStandInEnv turns this test binary into the installed binary's hook verb,
// so a test pane's hooks run the real dispatch.
const hookStandInEnv = "GI_TEST_HOOK_BIN"

// The stand-in carries the session's identity under names of its own: the
// test binary's isolation may clear the GATE_INBOX_* variables before
// TestMain runs.
var hookStandInCarry = map[string]string{
	"GI_TEST_SID":    hooks.EnvSessionID,
	"GI_TEST_STATUS": hooks.EnvStatusFile,
	"GI_TEST_BIN":    hooks.EnvExecutable,
}

func runHookStandIn() int {
	for from, to := range hookStandInCarry {
		os.Setenv(to, os.Getenv(from))
	}
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "hook" {
		return 0
	}
	_ = cli.RunHook(os.Stdin, os.Stdout, args[1:], os.Getenv(hooks.EnvSessionID), os.Getenv("GATE_INBOX_HOME"))
	return 0
}

// installHookStandIn writes an executable that runs this test binary as the
// hook verb, the way the installed gate-inbox would run.
func installHookStandIn(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var carry []string
	for from, to := range hookStandInCarry {
		carry = append(carry, from+`="$`+to+`"`)
	}
	bin := filepath.Join(t.TempDir(), "gate-inbox")
	script := "#!/bin/sh\n" + strings.Join(carry, " ") + " " + hookStandInEnv + "=1 exec '" + self + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

package config

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/deps"
)

// OutdatedToolError refuses a launch whose agent CLI is older than the
// oldest release the board supports (deps.Floor).
type OutdatedToolError struct {
	Binary  string
	Version string
	Floor   string
}

func (e OutdatedToolError) Error() string {
	text := e.Binary + " " + e.Version + " is older than " + e.Floor + ", the oldest version Gate Inbox supports"
	if command := deps.Command(e.Binary); command != "" {
		return text + "; upgrade it with: " + command
	}
	return text + "; upgrade it"
}

// runVersion runs `<path> --version`; tests replace it so no CLI runs.
var runVersion = func(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return string(out), err
}

// supported holds the resolved paths already seen at or above their floor.
// Only a passing answer is kept: the CLI can be upgraded under a running
// board, and the launch after an upgrade must see the new version, while a
// supported CLI does not go back down in any way worth a probe per launch.
var supported sync.Map

// CheckVersion refuses a command whose binary is older than deps.Floor for
// it. A binary with no floor, one not on PATH (CheckInstalled reports that)
// and one whose version cannot be read all pass: the floor exists to name a
// known-bad version, not to block a launch on a probe that failed.
func CheckVersion(command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}
	binary := filepath.Base(fields[0])
	floor := deps.Floor(binary)
	if floor == "" {
		return nil
	}
	path, err := lookPath(fields[0])
	if err != nil {
		return nil
	}
	if _, ok := supported.Load(path); ok {
		return nil
	}
	out, err := runVersion(path)
	if err != nil {
		return nil
	}
	version, ok := parseVersion(out)
	if !ok {
		return nil
	}
	if versionLess(version, floor) {
		return OutdatedToolError{Binary: binary, Version: version, Floor: floor}
	}
	supported.Store(path, struct{}{})
	return nil
}

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// parseVersion reads the first dotted version out of --version output
// ("1.5.1", "pi v0.85.1").
func parseVersion(out string) (string, bool) {
	version := versionPattern.FindString(out)
	return version, version != ""
}

// versionLess compares dotted numeric versions; a missing part counts as 0.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

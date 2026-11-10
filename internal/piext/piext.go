// Package piext is the pi extension the board loads into every pi session it
// launches. Pi has no hook config a launch can hand it, but it loads an
// extension for one run with `-e <path>` and touches nothing in ~/.pi for it,
// so the extension is the pi equivalent of the Claude plugin's hooks: it
// reports pi's lifecycle to `gate-inbox hook pi <event>` (hooks.DispatchPi),
// and every decision is made there.
package piext

import (
	_ "embed"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// source is the extension. Pi loads TypeScript through jiti, so it ships as
// written; there is no build step and nothing to install.
//
//go:embed extension.ts
var source []byte

// fileName is the extension's name before its content stamp
// (hooks.GeneratedName).
const fileName = "gate-inbox-pi-extension.ts"

// Source is the extension this build carries.
func Source() []byte { return source }

// Path is where this build's extension lives under hooksDir, written or not.
// It carries the content stamp every generated file's name does, so a build
// with a changed extension loads its own file and never one an older build
// left there.
func Path(hooksDir string) string {
	return filepath.Join(hooksDir, hooks.GeneratedName(fileName, source))
}

// Ensure writes this build's extension under hooksDir and returns its path.
// A file already there holds the same bytes, since the name is theirs, and
// is left alone.
//
// The sandbox runtime its bash tool loads is written beside it
// (EnsureSandbox).
func Ensure(hooksDir string) (string, error) {
	if err := EnsureSandbox(hooksDir); err != nil {
		return "", err
	}
	return hooks.WriteGenerated(hooksDir, fileName, source)
}

// Argv is the launch argument that loads the extension at quotedPath, which
// the caller has already shell-quoted.
func Argv(quotedPath string) string {
	return "-e " + quotedPath
}

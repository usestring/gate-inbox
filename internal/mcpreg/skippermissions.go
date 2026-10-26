package mcpreg

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// skipPermissionsJS is the opencode plugin a session launched with skip
// permissions on loads. It answers opencode's permission evaluate hook, which
// runs in the session's own server after every configured rule -- global,
// project and agent -- so it allows what a project's opencode.json or an agent
// would still ask about, and a subagent's requests as well as the session's.
// The TUI's --auto approves only the session on screen (anomalyco/opencode#44007),
// and a permissions rule in the generated config is overridden by any project
// or agent rule after it.
//
//go:embed skippermissions.js
var skipPermissionsJS []byte

// skipPermissionsPlugin is the directory holding that plugin, written under
// hooksDir unless write is false. v2 loads a configured local plugin only from
// a directory's index module, so the directory carries the content stamp
// every generated file's name does (hooks.GeneratedName).
func skipPermissionsPlugin(hooksDir string, write bool) (string, error) {
	dir := filepath.Join(hooksDir, hooks.GeneratedName(generatedPrefix+"opencode-skip-permissions", skipPermissionsJS))
	if !write {
		return dir, nil
	}
	path := filepath.Join(dir, "index.js")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, skipPermissionsJS) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".gen-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(skipPermissionsJS); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return dir, nil
}

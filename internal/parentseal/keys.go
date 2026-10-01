package parentseal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// Where the sealing keys live.
//
// Not in state.db. Every agent on the box runs as the same Unix user, and an
// agent's shell can read what that user can. The store has to stay readable,
// since sessions query it. The keys only have to be readable by Gate Inbox's
// own processes: the board that seals and the hook that checks. So they live
// in a directory of their own, 0700 with 0600 files, and every Claude Code
// session Gate Inbox launches is given settings that deny its sandboxed shell
// and its Read tool that directory (see hooks.KeyDirDenials). An agent whose
// sandbox is off, or one run outside Gate Inbox under the same user, can still
// read them; the threat model says so.

// KeyDirName is the directory under the config dir that holds the keys.
const KeyDirName = "channel-keys"

var validSession = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// KeyDir is the key directory for configDir.
func KeyDir(configDir string) string { return filepath.Join(configDir, KeyDirName) }

func keyPath(configDir, session string) (string, error) {
	if !validSession.MatchString(session) {
		return "", fmt.Errorf("not a session id: %q", session)
	}
	return filepath.Join(KeyDir(configDir), session), nil
}

// Key is session's sealing key, minted on first use.
func Key(configDir, session string) ([]byte, error) {
	if key, err := ExistingKey(configDir, session); err != nil || key != nil {
		return key, err
	}
	path, err := keyPath(configDir, session)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ExistingKey(configDir, session)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(fresh); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// ExistingKey is session's sealing key, or nil when none was ever minted:
// nothing was sealed to it, so nothing can verify.
func ExistingKey(configDir, session string) ([]byte, error) {
	path, err := keyPath(configDir, session)
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("sealing key for %s is %d bytes, want 32", session, len(key))
	}
	return key, nil
}

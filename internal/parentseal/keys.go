package parentseal

import (
	"bytes"
	"crypto/ed25519"
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
// since sessions query it. The signing key only has to be readable by the
// board, which seals. So it lives in a directory of its own, 0700 with 0600
// files, and every session Gate Inbox launches is kept out of that directory:
// a Claude Code session by settings that deny its sandboxed shell and its Read
// tool the directory (see hooks.KeyDirDenials), any other CLI by an OS-level
// confinement that leaves it only the public key, read-only. An agent whose
// sandbox is off, or one run outside Gate Inbox under the same user, can still
// read them; the threat model says so.

// KeyDirName is the directory under the config dir that holds the keys.
const KeyDirName = "channel-keys"

// PublicKeyFile is the public half of the board's seal key, the one file in
// the key directory a confined session's hook has to read.
const PublicKeyFile = "seal.pub"

// signingKeyFile holds the seed of the board's seal key.
const signingKeyFile = "seal.key"

var validSession = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// KeyDir is the key directory for configDir.
func KeyDir(configDir string) string { return filepath.Join(configDir, KeyDirName) }

// SigningKey is the board's seal key, minted on first use, with its public
// half written beside it for the hooks that check seals.
func SigningKey(configDir string) (ed25519.PrivateKey, error) {
	seed, err := mintKey(filepath.Join(KeyDir(configDir), signingKeyFile))
	if err != nil {
		return nil, err
	}
	key := ed25519.NewKeyFromSeed(seed)
	public := key.Public().(ed25519.PublicKey)
	written, err := writeOnce(filepath.Join(KeyDir(configDir), PublicKeyFile), public)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(written, public) {
		return nil, fmt.Errorf("%s does not match %s", PublicKeyFile, signingKeyFile)
	}
	return key, nil
}

// EnsurePublicKey makes the board's seal key unless its public half already
// exists.
func EnsurePublicKey(configDir string) error {
	if key, err := PublicKey(configDir); err != nil || key != nil {
		return err
	}
	_, err := SigningKey(configDir)
	return err
}

// PublicKey is the public half of the board's seal key, or nil when none was
// ever minted: nothing was sealed, so nothing can verify.
func PublicKey(configDir string) (ed25519.PublicKey, error) {
	return readKey(filepath.Join(KeyDir(configDir), PublicKeyFile))
}

// mintKey is the 32-byte key at path, written with fresh random bytes the
// first time it is asked for.
func mintKey(path string) ([]byte, error) {
	if key, err := readKey(path); err != nil || key != nil {
		return key, err
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	return writeOnce(path, fresh)
}

// writeOnce is the 32-byte key at path, written as key when there is none.
// The key is written whole under a name of its own and then linked into
// place, so a reader never sees a key half written, and two processes
// writing at once agree: the link that loses reads the winner's key.
func writeOnce(path string, key []byte) ([]byte, error) {
	if existing, err := readKey(path); err != nil || existing != nil {
		return existing, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, ".minting-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(key); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(file.Name(), path); errors.Is(err, fs.ErrExist) {
		return readKey(path)
	} else if err != nil {
		return nil, err
	}
	return key, nil
}

// readKey is the 32-byte key at path, or nil when it was never minted.
func readKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key %s is %d bytes, want 32", filepath.Base(path), len(key))
	}
	return key, nil
}

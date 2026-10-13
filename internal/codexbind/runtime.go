//go:build darwin || linux

package codexbind

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Cached daemon threads can retain launch configuration across resumes. Their
// runtime endpoint stays stable, but only the lock-owning live bridge serves it.
func (b *bridge) routeBinding(binding, destination string) error {
	path, token, ok := strings.Cut(binding, "#")
	if !ok || len(token) != 64 || filepath.Dir(filepath.Dir(path)) != "/tmp" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "gi-codex-runtime-") || filepath.Base(path) != "caller.sock" {
		return errors.New("invalid saved runtime binding")
	}
	if err := privateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New("runtime binding endpoint is not an owned alias")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(destination, path); err != nil {
		return err
	}
	if b.aliases == nil {
		b.aliases = map[string]string{}
	}
	b.aliases[path] = destination
	return nil
}

func (b *bridge) saveBinding(binding string) error {
	if b.lock == nil {
		return errors.New("runtime binding has no exclusive thread lock")
	}
	if err := b.lock.Truncate(0); err != nil {
		return err
	}
	if _, err := b.lock.Seek(0, 0); err != nil {
		return err
	}
	if _, err := b.lock.WriteString(binding); err != nil {
		return err
	}
	b.runtimeBinding = binding
	return nil
}

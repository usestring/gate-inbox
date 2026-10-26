//go:build darwin || linux

package gatechannel

import (
	"os"
	"path/filepath"
	"syscall"
)

func lockLease(configDir string) (func(), error) {
	if err := os.MkdirAll(Dir(configDir), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(Dir(configDir), "lease.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

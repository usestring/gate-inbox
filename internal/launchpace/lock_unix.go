//go:build darwin || linux

package launchpace

import (
	"os"
	"syscall"
)

// lockFile holds an exclusive lock on path until unlock.
func lockFile(path string) (unlock func(), err error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
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

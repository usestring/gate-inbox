// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package config

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/usestring/gate-inbox/internal/deps"
)

var lookPath = exec.LookPath

type MissingToolError struct {
	Binary string
}

func (e MissingToolError) Error() string {
	return e.Binary + " is not installed; " + deps.Hint(e.Binary)
}

func CheckInstalled(command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil
	}
	binary := fields[0]
	_, err := lookPath(binary)
	if err == nil {
		return nil
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return err
	}
	return MissingToolError{Binary: binary}
}

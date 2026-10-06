//go:build !darwin && !linux

package communications

import (
	"context"
	"errors"
)

func lock(context.Context, string) (func(), error) {
	return nil, errors.New("communications storage requires Linux, macOS, or WSL2")
}

//go:build !darwin && !linux

package gatechannel

import "errors"

func lockLease(string) (func(), error) {
	return nil, errors.New("gate channel lease locking is supported only on macOS and Linux")
}

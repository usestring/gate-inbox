//go:build !darwin && !linux

package launchpace

// Elsewhere the queue is shared without a lock: a race costs one launch
// that was not waited for.
func lockFile(string) (func(), error) {
	return func() {}, nil
}

//go:build !darwin && !linux

package accounts

// Elsewhere the listing is shared without a lock: a race costs one extra
// read of the secret store.
func lockFile(string) (func(), error) {
	return func() {}, nil
}

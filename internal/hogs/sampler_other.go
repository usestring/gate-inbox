//go:build !darwin

package hogs

// newNativeSource has nothing to offer here: Linux reads /proc.
func newNativeSource() nativeSource { return nil }

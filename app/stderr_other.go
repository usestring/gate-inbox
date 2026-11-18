//go:build !unix

package app

// redirectStderr is a no-op where fd 2 cannot be swapped with dup2.
func redirectStderr(string) func() { return func() {} }

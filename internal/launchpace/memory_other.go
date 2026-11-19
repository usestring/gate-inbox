//go:build !darwin

package launchpace

func hostMemoryPressure() bool { return false }

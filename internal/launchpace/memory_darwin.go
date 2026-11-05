package launchpace

import "golang.org/x/sys/unix"

func hostMemoryPressure() bool {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	// Use the kernel's warning/critical signal: free RAM alone excludes the
	// memory macOS can reclaim, and cannot tell whether a launch is affordable.
	return err == nil && (level == 2 || level == 4)
}

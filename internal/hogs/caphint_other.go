//go:build !darwin

package hogs

// The ways a notice offers to cap a load: systemd scopes on Linux.
const (
	cpuCapHint = "`systemd-run --user --scope -p CPUQuota=200% <command>`, or `renice -n 19 -p <pid>` for one already running"
	memCapHint = "`systemd-run --user --scope -p MemoryMax=8G <command>`, or a smaller worker count"
)

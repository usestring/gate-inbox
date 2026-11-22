//go:build darwin

package hogs

// The ways a notice offers to cap a load on macOS, which has no systemd and
// enforces no per-process memory limit (ulimit -v is accepted and ignored).
// taskpolicy -b runs a command at background QoS, confined to the efficiency
// cores; renice only lowers priority against other work.
const (
	cpuCapHint = "`taskpolicy -b <command>`, or `renice -n 19 -p <pid>` for one already running"
	memCapHint = "a smaller worker count, or the runtime's own heap limit such as `GOMEMLIMIT=4GiB` or `node --max-old-space-size=4096`"
)

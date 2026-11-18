//go:build !darwin

package sysstat

// nativeTrees has no implementation here: Linux walks /proc in TreeSampler,
// and anything else falls back to ps.
func nativeTrees([]int) (map[int]ProcStat, bool) {
	return nil, false
}

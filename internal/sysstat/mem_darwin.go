// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

//go:build darwin

package sysstat

import (
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/sys/unix"
)

// sampleMemory on macOS reports the kernel's own estimate of memory in use:
// 100 minus kern.memorystatus_level, the share of RAM the kernel says it can
// still hand out, counting file cache and idle app memory it can reclaim.
// Activity Monitor's "Memory Used" counts that reclaimable memory as used, so
// it read ~70% on a Mac the kernel rated ~60% free under normal pressure.
func sampleMemory(snap *Snapshot) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		sampleMemoryFallback(snap)
		return
	}
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil || level > 100 {
		sampleMemoryFallback(snap)
		return
	}
	snap.MemTotal = total
	snap.MemUsed = memUsedFromLevel(total, level)
	snap.MemPercent = usedPercent(snap.MemUsed, total)
	snap.MemOK = true
}

func memUsedFromLevel(total uint64, availablePercent uint32) uint64 {
	return total / 100 * uint64(100-availablePercent)
}

func sampleMemoryFallback(snap *Snapshot) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	snap.MemTotal = vm.Total
	if vm.Total >= vm.Available {
		snap.MemUsed = vm.Total - vm.Available
	} else {
		snap.MemUsed = vm.Used
	}
	snap.MemPercent = usedPercent(snap.MemUsed, snap.MemTotal)
	snap.MemOK = true
}

const (
	// swapVolume holds the swapfiles the kernel creates under pressure.
	swapVolume = "/System/Volumes/VM"
	// maxSwapFiles is XNU's VM_MAX_SWAP_FILE_NUM; each file is 1 GiB.
	maxSwapFiles = 100
	swapFileSize = 1 << 30
)

// swapCeiling is how far swap can grow: what is allocated now plus the free
// space the kernel can still turn into swapfiles, capped at the kernel's file
// limit. It falls back to the allocation when the volume cannot be read.
func swapCeiling(allocated uint64) uint64 {
	var st unix.Statfs_t
	if err := unix.Statfs(swapVolume, &st); err != nil {
		return allocated
	}
	return clampSwapCeiling(allocated, st.Bavail*uint64(st.Bsize))
}

func clampSwapCeiling(allocated, free uint64) uint64 {
	ceiling := allocated + free
	if limit := uint64(maxSwapFiles * swapFileSize); ceiling > limit {
		ceiling = limit
	}
	if ceiling < allocated {
		ceiling = allocated
	}
	return ceiling
}

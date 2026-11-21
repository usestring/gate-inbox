package store

// MemoryGuardSetting turns on the relaunch queue's macOS memory-pressure
// guard. It is an experiment, off by default: the kernel reports "warning"
// once swap fills, even with a quarter of RAM free, and the guard then
// refused every revive of a hibernated session while the board's own
// hibernation already keeps the agent count inside its memory budget.
const MemoryGuardSetting = "experimental_memory_guard"

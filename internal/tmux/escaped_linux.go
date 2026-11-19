package tmux

import (
	"os"
	"strconv"
	"strings"
)

// readEscapedProcs reads every process this user may inspect. A process
// whose environment cannot be read belongs to someone else and is skipped.
func readEscapedProcs() []escapedProc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []escapedProc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		dir := "/proc/" + entry.Name() + "/"
		environ, err := os.ReadFile(dir + "environ")
		if err != nil {
			continue
		}
		tag := tagOf(environ)
		if tag == "" {
			continue
		}
		ppid, start, ok := statOf(pid)
		if !ok {
			continue
		}
		cmdline, _ := os.ReadFile(dir + "cmdline")
		out = append(out, escapedProc{
			pid:   pid,
			ppid:  ppid,
			start: start,
			tag:   tag,
			args:  strings.ReplaceAll(string(cmdline), "\x00", " "),
		})
	}
	return out
}

// startOf is pid's start time in clock ticks since boot.
func startOf(pid int) (uint64, bool) {
	_, start, ok := statOf(pid)
	return start, ok
}

// parentOf is pid's parent.
func parentOf(pid int) (int, bool) {
	ppid, _, ok := statOf(pid)
	return ppid, ok
}

func statOf(pid int) (ppid int, start uint64, ok bool) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	return parseStat(string(stat))
}

// parseStat reads the parent pid and start time out of /proc/<pid>/stat.
// The command name can hold spaces and parentheses, so fields are counted
// from the last closing parenthesis.
func parseStat(stat string) (ppid int, start uint64, ok bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is the state (field 3); ppid is field 4, starttime field 22.
	if len(fields) < 20 {
		return 0, 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	start, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ppid, start, true
}

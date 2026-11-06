package singleton

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrNoHolder means no running manager holds dir's claim.
var ErrNoHolder = errors.New("no running board holds the lock")

// Holder returns the pid of the manager that holds dir's claim right now.
//
// The pid written in the lock file is not enough on its own: the file
// outlives a killed board, and a signal sent to a reused pid lands on an
// unrelated process. The kernel's lock table names the process that actually
// holds the flock, so that is what is read. It is Linux only; elsewhere the
// caller is told so rather than handed a guess.
func Holder(dir string) (int, error) {
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNoHolder
		}
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read the inode of %s on this platform", FileName)
	}
	locks, err := os.Open("/proc/locks")
	if err != nil {
		return 0, fmt.Errorf("cannot read the kernel lock table: %w", err)
	}
	defer locks.Close()
	dev := uint64(stat.Dev) //nolint:unconvert // Dev is uint32 on some platforms
	// The kernel prints the device as major:minor in hex, from the same
	// encoding the stat call returned it in.
	device := fmt.Sprintf("%02x:%02x", (dev>>8)&0xfff, (dev&0xff)|((dev>>12)&0xfff00))
	return holderIn(bufio.NewScanner(locks), device, uint64(stat.Ino))
}

// holderIn finds the FLOCK on inode in /proc/locks lines, which read
//
//	1: FLOCK  ADVISORY  WRITE 12345 fd:01:6789 0 EOF
//
// with the device and inode as major:minor:inode.
func holderIn(lines *bufio.Scanner, device string, inode uint64) (int, error) {
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		// A blocked waiter is printed as "1: -> FLOCK ...", one field longer;
		// it does not hold the lock, so it is skipped by shape.
		if len(fields) < 6 || fields[1] != "FLOCK" {
			continue
		}
		dev := strings.Split(fields[5], ":")
		if len(dev) != 3 || dev[0]+":"+dev[1] != device {
			continue
		}
		ino, err := strconv.ParseUint(dev[2], 10, 64)
		if err != nil || ino != inode {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 {
			continue
		}
		return pid, nil
	}
	if err := lines.Err(); err != nil {
		return 0, err
	}
	return 0, ErrNoHolder
}

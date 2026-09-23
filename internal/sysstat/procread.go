package sysstat

import (
	"sync"
	"syscall"
)

// This file is how the tree walk touches /proc, and it is deliberately not
// os.ReadFile.
//
// The walk opens ~2,100 files a pass -- a stat per process plus a children
// list per thread -- and os.ReadFile costs 6.6 syscalls for each one: the
// openat and read and close the work needs, plus an fstat to size the buffer,
// a failing epoll_ctl (the runtime tries to register every opened file with
// the netpoller, and a /proc file is not pollable), and ~3.8 fcntls. None of
// those six are wrong on their own; at 2,100 files every two seconds they are
// 14,000 syscalls a pass spent on nothing the sampler reads. Going straight to
// the kernel leaves 3 per file and answers exactly the same bytes, which is
// what TestTheRawReaderAgreesWithReadFile holds it to.
//
// The buffers are pooled rather than allocated per file for the same reason:
// the walk reads thousands of files and throws every one of them away.

// procBufSize is comfortably past both shapes read here -- a stat line runs
// ~350 bytes and a children list a few hundred -- so the common file is one
// read. Anything longer grows the buffer and reads again.
const procBufSize = 4096

var procBufs = sync.Pool{New: func() any {
	buf := make([]byte, procBufSize)
	return &buf
}}

// withProcFile reads one /proc file and hands the bytes to parse, which must
// not keep them: the buffer goes back to the pool on return.
//
// It reports false when the file could not be opened, which on this walk
// means the process exited between being listed and being read -- the
// commonest thing that happens to a /proc path and never an error worth
// reporting.
func withProcFile(path string, parse func([]byte)) bool {
	fd, ok := openProc(path, syscall.O_RDONLY)
	if !ok {
		return false
	}
	buf := procBufs.Get().(*[]byte)
	body := (*buf)[:0]
	for {
		if len(body) == cap(body) {
			grown := make([]byte, len(body), cap(body)*2)
			copy(grown, body)
			body = grown
		}
		n, err := syscall.Read(fd, body[len(body):cap(body)])
		if err == syscall.EINTR {
			continue
		}
		if n > 0 {
			body = body[:len(body)+n]
		}
		// A short read ends it. These are seq_file files: the kernel fills
		// the buffer until its rendering is exhausted, so anything less than
		// what was asked for is the end of the file, and reading again only
		// to be told zero would put a syscall back on every file.
		if err != nil || n <= 0 || len(body) < cap(body) {
			break
		}
	}
	syscall.Close(fd)
	parse(body)
	// Only a buffer that is still the pooled one goes back; a grown read
	// replaced it, and returning the larger one would let one oversized file
	// set the size every later read allocates against.
	if cap(body) == cap(*buf) {
		procBufs.Put(buf)
	}
	return true
}

// openProc opens a /proc path for reading, retrying the one error that is
// not an answer: a signal landing mid-call. os.ReadFile does the same, out of
// sight, and a Go program takes preemption signals constantly.
//
// O_CLOEXEC matters more here than in most places: the manager forks tmux
// tens of times a pass, and a descriptor left open across an exec would be
// inherited by every one of them.
func openProc(path string, flags int) (int, bool) {
	for {
		fd, err := syscall.Open(path, flags|syscall.O_CLOEXEC, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return 0, false
		}
		return fd, true
	}
}

// procDirNames lists a /proc directory's entries without stat-ing any of
// them. os.ReadDir would open through the same six-syscall path as ReadFile
// and then sort the result, and the task directory this reads is never
// ordered on.
func procDirNames(path string, each func(name string)) bool {
	fd, ok := openProc(path, syscall.O_RDONLY|syscall.O_DIRECTORY)
	if !ok {
		return false
	}
	buf := procBufs.Get().(*[]byte)
	names := make([]string, 0, 16)
	for {
		n, err := syscall.ReadDirent(fd, *buf)
		if err == syscall.EINTR {
			continue
		}
		if n <= 0 || err != nil {
			break
		}
		_, _, names = syscall.ParseDirent((*buf)[:n], -1, names[:0])
		for _, name := range names {
			each(name)
		}
	}
	syscall.Close(fd)
	procBufs.Put(buf)
	return true
}

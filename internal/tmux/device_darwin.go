package tmux

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

func clientEnvironment(pid int32) ([]string, error) {
	args, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil {
		return nil, err
	}
	return environmentFromProcArgs(args)
}

func environmentFromProcArgs(data []byte) ([]string, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("missing process argument count")
	}
	argc := int32(binary.NativeEndian.Uint32(data[:4]))
	if argc <= 0 {
		return nil, fmt.Errorf("invalid process argument count")
	}
	_, args, ok := bytes.Cut(data[4:], []byte{0})
	if !ok {
		return nil, fmt.Errorf("unterminated process executable path")
	}
	args = bytes.TrimLeft(args, "\x00")
	for range argc {
		_, rest, ok := bytes.Cut(args, []byte{0})
		if !ok {
			return nil, fmt.Errorf("truncated process arguments")
		}
		args = rest
	}
	var env []string
	for len(args) > 0 && args[0] != 0 {
		entry, rest, ok := bytes.Cut(args, []byte{0})
		if !ok {
			return nil, fmt.Errorf("unterminated process environment entry")
		}
		env = append(env, string(entry))
		args = rest
	}
	return env, nil
}

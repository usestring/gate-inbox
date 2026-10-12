package codexbind

import (
	"bytes"
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

func initialEnvironment(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, errors.New("process launch arguments unavailable")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc < 1 || argc > 65536 {
		return nil, errors.New("invalid process launch arguments")
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return nil, errors.New("process launch path unavailable")
	}
	raw = raw[end+1:]
	for len(raw) > 0 && raw[0] == 0 {
		raw = raw[1:]
	}
	for range argc {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return nil, errors.New("process launch argument truncated")
		}
		raw = raw[end+1:]
	}
	var env []string
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) > 0 {
			env = append(env, string(entry))
		}
	}
	return env, nil
}

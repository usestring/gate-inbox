//go:build !darwin

package tmux

import "github.com/shirou/gopsutil/v4/process"

func clientEnvironment(pid int32) ([]string, error) {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, err
	}
	return proc.Environ()
}

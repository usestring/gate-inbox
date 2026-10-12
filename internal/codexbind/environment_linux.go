package codexbind

import (
	"bytes"
	"os"
	"strconv"
)

func initialEnvironment(pid int) ([]string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, err
	}
	var env []string
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) > 0 {
			env = append(env, string(entry))
		}
	}
	return env, nil
}

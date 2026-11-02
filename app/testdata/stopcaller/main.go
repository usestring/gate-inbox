package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

func main() {
	args := []string{"stop", "--if-managed", "--json"}
	if os.Getenv("FIXTURE_STOP_REAL") == "1" {
		args = append(args, "--archive")
	} else {
		args = append(args, "--dry-run")
	}
	cmd := exec.Command(os.Args[2], args...)
	out, err := cmd.CombinedOutput()
	result := struct {
		Output string
		Failed bool
	}{string(out), err != nil}
	data, _ := json.Marshal(result)
	if err := os.WriteFile(os.Args[3], data, 0600); err != nil {
		panic(err)
	}
	time.Sleep(time.Minute)
}

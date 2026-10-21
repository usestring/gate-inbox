package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

func main() {
	cmd := exec.Command(os.Args[2], "stop", "--if-managed", "--dry-run", "--json")
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

package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

const deviceClientFormat = "#{client_pid}\t#{client_activity}\t#{client_flags}\t#{client_control_mode}\t#{client_readonly}\t#{pane_id}"

type deviceClient struct {
	pid      int32
	activity int64
	focused  bool
}

func controllingClient(reply, pane string) (deviceClient, error) {
	var selected deviceClient
	for _, line := range strings.Split(strings.TrimSuffix(reply, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			return deviceClient{}, fmt.Errorf("invalid tmux client response")
		}
		if fields[3] != "0" || fields[4] != "0" || fields[5] != pane {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil || pid <= 0 {
			return deviceClient{}, fmt.Errorf("invalid tmux client pid %q", fields[0])
		}
		activity, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return deviceClient{}, fmt.Errorf("invalid tmux client activity %q", fields[1])
		}
		candidate := deviceClient{pid: int32(pid), activity: activity}
		for _, flag := range strings.Split(fields[2], ",") {
			if flag == "focused" {
				candidate.focused = true
			}
		}
		if selected.pid == 0 || candidate.focused && !selected.focused || candidate.focused == selected.focused && candidate.activity > selected.activity {
			selected = candidate
		}
	}
	return selected, nil
}

// ClientDeviceAt reads the attaching process, since a persistent pane's SSH
// environment belongs to whichever device originally created it.
func (d *Driver) ClientDeviceAt(socket, pane string) (string, error) {
	command := `list-clients -F "` + deviceClientFormat + `"`
	var reply string
	if control, err := d.captures.client(d, socket); err == nil && control != nil {
		reply, err = control.Command(command)
		if err != nil {
			return "", err
		}
	} else {
		out, err := d.output([]string{"-L", socket, "list-clients", "-F", deviceClientFormat})
		if err != nil {
			return "", err
		}
		reply = string(out)
	}
	client, err := controllingClient(reply, pane)
	if err != nil || client.pid == 0 {
		return "", err
	}
	env, err := clientEnvironment(client.pid)
	if err != nil {
		return "", fmt.Errorf("read controlling device: %w", err)
	}
	return DeviceIdentity(env), nil
}

func DeviceIdentity(env []string) string {
	values := make(map[string]string)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if name := values["GATE_INBOX_DEVICE"]; name != "" {
		return "device:" + name
	}
	terminal := values["TERM_PROGRAM"]
	if terminal == "" {
		terminal = values["TERM"]
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT"} {
		if fields := strings.Fields(values[key]); len(fields) > 0 {
			return "ssh:" + fields[0] + "/" + terminal
		}
	}
	if terminal != "" {
		return "local:" + terminal
	}
	return ""
}

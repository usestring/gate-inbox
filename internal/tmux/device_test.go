package tmux

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestDeviceIdentitySurvivesReconnect(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"named", []string{"GATE_INBOX_DEVICE=ipad", "SSH_CONNECTION=100.64.0.2 1111 100.64.0.1 22"}, "device:ipad"},
		{"ssh", []string{"SSH_CONNECTION=100.64.0.2 1111 100.64.0.1 22", "TERM_PROGRAM=Termius"}, "ssh:100.64.0.2/Termius"},
		{"reconnected", []string{"SSH_CONNECTION=100.64.0.2 2222 100.64.0.1 22", "TERM_PROGRAM=Termius"}, "ssh:100.64.0.2/Termius"},
		{"another device", []string{"SSH_CONNECTION=100.64.0.3 1111 100.64.0.1 22", "TERM_PROGRAM=Termius"}, "ssh:100.64.0.3/Termius"},
		{"ssh client", []string{"SSH_CLIENT=100.64.0.2 1111 22", "TERM=xterm-256color"}, "ssh:100.64.0.2/xterm-256color"},
		{"local", []string{"TERM_PROGRAM=ghostty", "TERM=xterm-ghostty"}, "local:ghostty"},
		{"unknown", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeviceIdentity(tc.env); got != tc.want {
				t.Fatalf("identity = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestControllingClientFollowsFocusThenActivity(t *testing.T) {
	cases := []struct {
		name, reply string
		want        int32
	}{
		{"latest", "101\t100\t\t0\t0\t%1\n102\t200\t\t0\t0\t%1\n", 102},
		{"focused", "101\t100\tfocused\t0\t0\t%1\n102\t200\t\t0\t0\t%1\n", 101},
		{"both focused", "101\t100\tfocused\t0\t0\t%1\n102\t200\tfocused\t0\t0\t%1\n", 102},
		{"exclude control readonly and other panes", "101\t100\t\t0\t0\t%1\n102\t200\tfocused\t1\t0\t%1\n103\t300\tfocused\t0\t1\t%1\n104\t400\tfocused\t0\t0\t%2\n", 101},
		{"unattached control", "102\t200\tcontrol-mode\t1\t0\t\n", 0},
		{"detached", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := controllingClient(tc.reply, "%1")
			if err != nil {
				t.Fatal(err)
			}
			if got.pid != tc.want {
				t.Fatalf("client pid = %d, want %d", got.pid, tc.want)
			}
		})
	}
	if _, err := controllingClient("bad reply", "%1"); err == nil {
		t.Fatal("accepted malformed client response")
	}
}

func TestDeviceIdentityReadsAttachingProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestDeviceEnvironmentChild$")
	cmd.Env = []string{"GATE_INBOX_DEVICE_TEST_CHILD=1", "GATE_INBOX_DEVICE=test-tablet", "TERM=xterm-256color"}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	env, err := clientEnvironment(int32(cmd.Process.Pid))
	if err != nil {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			t.Fatal(err)
		}
		t.Skipf("process environment unavailable: %v", err)
	}
	if got := DeviceIdentity(env); got != "device:test-tablet" {
		t.Fatalf("device = %q", got)
	}
}

func TestDeviceEnvironmentChild(t *testing.T) {
	if os.Getenv("GATE_INBOX_DEVICE_TEST_CHILD") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestClientDeviceLiveProbe(t *testing.T) {
	socket := os.Getenv("GATE_INBOX_DEVICE_PROBE_SOCKET")
	if socket == "" {
		t.Skip("set GATE_INBOX_DEVICE_PROBE_SOCKET for an isolated attached-client probe")
	}
	if !strings.HasPrefix(socket, "gitest-device-probe-") {
		t.Fatal("probe must use a test-owned socket")
	}
	driver, err := NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(driver.CloseCaptureClients)
	device, err := driver.ClientDeviceAt(socket, os.Getenv("GATE_INBOX_DEVICE_PROBE_PANE"))
	if err != nil {
		t.Fatal(err)
	}
	if want := os.Getenv("GATE_INBOX_DEVICE_PROBE_WANT"); device != want {
		t.Fatalf("device = %q, want %q", device, want)
	}
}

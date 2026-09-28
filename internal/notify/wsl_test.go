// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package notify

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// WSL has no notification daemon of its own; the banner is a Windows toast
// posted through the interop PowerShell, with the text carried in the
// environment rather than the command line.
func TestSendWSLPostsWindowsToast(t *testing.T) {
	defer restore()()
	goos = "linux"
	getenv = func(string) string { return "" }
	isWSL = func() bool { return true }
	rec := &cmdRecorder{known: map[string]bool{"powershell.exe": true, "notify-send": true}}
	rec.install()
	emitSeq = func(string) error { return nil }
	Send(Note{Subject: "deploy · claude", Body: body(Errored), Kind: Errored})
	if len(rec.called) != 1 || !strings.HasSuffix(rec.called[0][0], "/powershell.exe") {
		t.Fatalf("want one powershell call, got %v", rec.called)
	}
	call := rec.called[0]
	if !slices.Equal(call[1:4], []string{"-NoProfile", "-NonInteractive", "-EncodedCommand"}) || len(call) != 5 {
		t.Fatalf("unexpected powershell invocation %v", call)
	}
	if call[4] != encodedCommand(toastScript) {
		t.Fatal("the encoded command should be the toast script")
	}
	want := map[string]string{
		"GATE_INBOX_TOAST_TITLE":    appName,
		"GATE_INBOX_TOAST_SUBTITLE": "deploy · claude",
		"GATE_INBOX_TOAST_BODY":     body(Errored),
		"GATE_INBOX_TOAST_SOUND":    "ms-winsoundevent:Notification.IM",
		"GATE_INBOX_TOAST_APPID":    toastAppID,
	}
	for key, value := range want {
		if rec.envs[0][key] != value {
			t.Fatalf("%s = %q, want %q", key, rec.envs[0][key], value)
		}
	}
}

func TestSendWSLWithoutPowerShellOnPathUsesTheWindowsCopy(t *testing.T) {
	defer restore()()
	goos = "linux"
	getenv = func(string) string { return "" }
	isWSL = func() bool { return true }
	rec := &cmdRecorder{known: map[string]bool{}}
	rec.install()
	emitSeq = func(string) error { return nil }
	Send(Note{Subject: "deploy · claude", Body: body(Waiting), Kind: Waiting})
	if len(rec.called) != 1 || rec.called[0][0] != powershellFallback {
		t.Fatalf("want the System32 powershell, got %v", rec.called)
	}
}

// A toast that fails still leaves the bell, as every other path does.
func TestSendWSLFailedToastRingsTheBell(t *testing.T) {
	defer restore()()
	goos = "linux"
	getenv = func(string) string { return "" }
	isWSL = func() bool { return true }
	rec := &cmdRecorder{known: map[string]bool{"powershell.exe": true}, runErr: errors.New("toast refused")}
	rec.install()
	var emitted []string
	emitSeq = func(seq string) error {
		emitted = append(emitted, seq)
		return nil
	}
	Send(Note{Body: "hello"})
	if !slices.Equal(emitted, []string{"\a"}) {
		t.Fatalf("want the bell after a failed toast, got %q", emitted)
	}
}

// PowerShell reads -EncodedCommand as base64 over UTF-16LE.
func TestEncodedCommandIsUTF16LEBase64(t *testing.T) {
	got := encodedCommand("Ab€")
	if got != "QQBiAKwg" {
		t.Fatalf("encodedCommand = %q, want QQBiAKwg", got)
	}
}

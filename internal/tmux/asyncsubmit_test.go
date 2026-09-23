// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"os"
	"strings"
	"testing"
	"time"
)

// stubDriver is a Driver over a tmux that answers every capture with the
// same pane and logs every call. A capture that never shows the pasted text
// is a pane that never draws it, which is the case the echo window exists
// for and the only case that costs anything.
func stubDriver(t *testing.T, window time.Duration) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	callLog := dir + "/calls"
	stub := dir + "/tmux"
	script := "#!/bin/sh\necho \"$@\" >> " + callLog + "\n" +
		"case \"$*\" in *capture-pane*) echo 'an empty composer';; esac\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("stub: %v", err)
	}
	return &Driver{bin: stub, socket: testSocket, echoWaitOverride: window}, callLog
}

func stubCalls(t *testing.T, callLog string) string {
	t.Helper()
	logged, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	return string(logged)
}

// The point of the whole change: the caller is off the window.
//
// The pane here never draws the paste, so the Enter cannot go out until the
// window is spent -- and SendTextAsync must still be back long before it is.
// The window is overridden rather than waited out; its production length is
// pinned by TestPasteWindowDefaultsToTheProductionEchoWait.
func TestSendTextAsyncReturnsWithoutWaitingOutThePasteWindow(t *testing.T) {
	// The floor is the ~100ms the forks below cost: a window under that
	// would be satisfied by the overhead alone.
	const window = 2 * time.Second
	driver, callLog := stubDriver(t, window)

	submitted := make(chan error, 1)
	start := time.Now()
	if err := driver.SendTextAsync("x1", "hello world", func(err error) { submitted <- err }); err != nil {
		t.Fatalf("SendTextAsync: %v", err)
	}
	handoff := time.Since(start)
	if handoff > window/2 {
		t.Fatalf("SendTextAsync took %v of a %v window before returning: the caller is still paying for the pane", handoff, window)
	}
	if strings.Contains(stubCalls(t, callLog), "Enter") {
		t.Fatal("the Enter went out before the pane was given its window: a pane too busy to read " +
			"between the two writes takes that carriage return as part of the paste and the message is never submitted")
	}

	select {
	case err := <-submitted:
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	case <-time.After(window + 2*time.Second):
		t.Fatal("the deferred submit never fired: the text is in the pane and nothing will ever send it")
	}
	elapsed := time.Since(start)
	if elapsed < window {
		t.Fatalf("the Enter went out after %v, want the pane to get its full %v", elapsed, window)
	}
	if !strings.Contains(stubCalls(t, callLog), "send-keys -t gi_x1 Enter") {
		t.Fatalf("Enter never sent, calls:\n%s", stubCalls(t, callLog))
	}
}

// The guarantee, against a real pane that reads late.
//
// This is TestSendTextSubmitsIntoAPaneThatReadsLate's fixture driven the
// asynchronous way: the Enter has to reach the pane as a read of its own,
// after the one that ends the bracketed paste, however long the pane takes
// to get around to reading. Deferring the wait must not weaken that.
func TestSendTextAsyncStillSubmitsIntoAPaneThatReadsLate(t *testing.T) {
	driver := requireTmux(t)
	id := "asynclate" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	reads := "/tmp/gi-asynclate-" + id
	t.Cleanup(func() { os.Remove(reads) })

	// Opens on a blank line, which still has to be waited for.
	text := "\nhello world"
	command := "stty raw -echo; printf '\\033[?2004h'; sleep 0.4; " +
		"while :; do dd bs=4096 count=1 2>/dev/null | tee -a " + ShellQuote(reads) +
		"; printf '|' >> " + ShellQuote(reads) + "; done"
	if err := driver.Create(id, "/tmp", command, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	time.Sleep(100 * time.Millisecond)
	submitted := make(chan error, 1)
	if err := driver.SendTextAsync(id, text, func(err error) { submitted <- err }); err != nil {
		t.Fatalf("SendTextAsync: %v", err)
	}
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the deferred submit never fired")
	}

	want := "\x1b[201~|\r"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := os.ReadFile(reads); err == nil && strings.Contains(string(got), want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := os.ReadFile(reads)
	t.Fatalf("pane reads = %q, want the paste to end a read (%q) before the Enter", got, want)
}

// What a deferred submit makes newly possible, and must not allow: a pane
// left mid-send. Between the paste and the Enter it owes, that pane holds
// text nothing has submitted yet -- so a second paste arriving in the gap
// would be swept up by the first paste's Enter and the two would go in as
// one message.
//
// So a paste waits for the submit the pane is owed. The call log is the
// assertion: the first Enter must sit between the two pastes.
func TestASecondPasteWaitsForTheEnterTheFirstIsOwed(t *testing.T) {
	const window = 400 * time.Millisecond
	driver, callLog := stubDriver(t, window)

	submitted := make(chan error, 1)
	if err := driver.SendTextAsync("x1", "first message", func(err error) { submitted <- err }); err != nil {
		t.Fatalf("SendTextAsync: %v", err)
	}
	// Straight into the gap, the way a second send on a later pass would.
	if err := driver.Paste("x1", "second message"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if err := <-submitted; err != nil {
		t.Fatalf("submit: %v", err)
	}

	calls := stubCalls(t, callLog)
	var order []string
	for _, line := range strings.Split(calls, "\n") {
		switch {
		case strings.Contains(line, "paste-buffer"):
			order = append(order, "paste")
		case strings.Contains(line, "Enter"):
			order = append(order, "enter")
		}
	}
	want := "paste enter paste"
	if got := strings.Join(order, " "); got != want {
		t.Fatalf("pane writes went out as %q, want %q: a paste landed in a pane still owed an Enter, "+
			"so that Enter submits both messages as one\ncalls:\n%s", got, want, calls)
	}
}

// A submit waited out by its own goroutine must not leave the pane marked
// mid-send afterwards, or the next paste into it waits on something that
// already happened -- or never returns at all.
func TestAPaneIsReleasedOnceItsSubmitHasGoneOut(t *testing.T) {
	const window = 100 * time.Millisecond
	driver, _ := stubDriver(t, window)

	submitted := make(chan error, 1)
	if err := driver.SendTextAsync("x1", "first message", func(err error) { submitted <- err }); err != nil {
		t.Fatalf("SendTextAsync: %v", err)
	}
	if err := <-submitted; err != nil {
		t.Fatalf("submit: %v", err)
	}
	reclaimed := make(chan struct{})
	go func() {
		driver.releasePane("x1", driver.claimPane("x1"))
		close(reclaimed)
	}()
	select {
	case <-reclaimed:
	case <-time.After(2 * time.Second):
		t.Fatal("a pane whose Enter has already gone out is still marked mid-send: every later write into it blocks forever")
	}
}

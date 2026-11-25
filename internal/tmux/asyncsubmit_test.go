// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"errors"
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
	if err := driver.SendTextAsync("x1", "hello world", nil, func(err error) { submitted <- err }); err != nil {
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
	if err := driver.SendTextAsync(id, text, nil, func(err error) { submitted <- err }); err != nil {
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

// A pane that put a dialog up after the caller last looked swallows the
// paste and would take the Enter as its answer, ticking or picking the row
// under its cursor. Shown that pane, hold keeps the Enter from going out.
func TestSendTextAsyncLeavesTheEnterUnpressedOnADialog(t *testing.T) {
	driver := requireTmux(t)
	id := "asyncheld" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	reads := "/tmp/gi-asyncheld-" + id
	t.Cleanup(func() { os.Remove(reads) })

	legend := "Enter to select · ↑/↓ to navigate · Esc to cancel"
	command := "stty raw -echo; printf '\\033[?2004h'; printf '%s' " + ShellQuote(legend) + "; " +
		"while :; do dd bs=4096 count=1 2>/dev/null >> " + ShellQuote(reads) + "; done"
	if err := driver.Create(id, "/tmp", command, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	time.Sleep(200 * time.Millisecond)
	// The legend is on screen from the start, so the look before the paste
	// is made to miss it. That is the pane as it was when the dialog arrived
	// between the paste and the Enter.
	looks := 0
	hold := func(pane string) bool {
		looks++
		return looks > 1 && strings.Contains(pane, legend)
	}
	submitted := make(chan error, 1)
	if err := driver.SendTextAsync(id, "rebase on main", hold, func(err error) { submitted <- err }); err != nil {
		t.Fatalf("SendTextAsync: %v", err)
	}
	select {
	case err := <-submitted:
		if !errors.Is(err, ErrSubmitHeld) {
			t.Fatalf("submit = %v, want ErrSubmitHeld", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the deferred submit never settled")
	}
	time.Sleep(200 * time.Millisecond)
	got, _ := os.ReadFile(reads)
	if !strings.Contains(string(got), "rebase on main") {
		t.Fatalf("pane reads = %q, want the paste to have arrived", got)
	}
	if strings.Contains(string(got), "\r") {
		t.Fatalf("pane reads = %q: Enter reached a pane showing a dialog", got)
	}
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
	if err := driver.SendTextAsync("x1", "first message", nil, func(err error) { submitted <- err }); err != nil {
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
	if err := driver.SendTextAsync("x1", "first message", nil, func(err error) { submitted <- err }); err != nil {
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

func TestSubmitDoesNotPressEnterWhenSafetyCaptureFails(t *testing.T) {
	driver, callLog := stubDriver(t, time.Second)
	dir := t.TempDir()
	marker := ShellQuote(dir + "/echoed")
	ready := ShellQuote(dir + "/ready")
	script := "#!/bin/sh\necho \"$@\" >> " + ShellQuote(callLog) + "\n" +
		"case \"$*\" in *capture-pane*)\n" +
		"if [ -e " + marker + " ]; then exit 1; fi\n" +
		"if [ -e " + ready + " ]; then touch " + marker + "; echo 'hello world'; " +
		"else touch " + ready + "; echo 'an empty composer'; fi\n;; esac\n"
	if err := os.WriteFile(driver.bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The first look, before the paste, sees a pane that captured. Only a
	// second look would be shown the failed capture.
	looks := 0
	submit, err := driver.pasteHoldingSubmit("x1", "hello world", func(string) bool {
		looks++
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := submit(); err == nil {
		t.Error("submit succeeded without checking whether the pane shows a dialog")
	}
	if looks > 1 {
		t.Error("hold received a pane that could not be captured")
	}
	if strings.Contains(stubCalls(t, callLog), "send-keys") {
		t.Fatalf("sent a key without a safety capture:\n%s", stubCalls(t, callLog))
	}
}

// A pane already showing a dialog when the paste is due gets no paste at
// all. Holding only the Enter is too late for a question dialog. Its
// free-text row takes the paste as typing, so the dialog is left with
// someone else's message where the operator's answer goes. Nothing is
// pasted and nothing is submitted, and the caller hears ErrPasteHeld at once
// so it can leave the message queued.
func TestSendTextAsyncPastesNothingIntoAPaneAlreadyOnADialog(t *testing.T) {
	driver, callLog := stubDriver(t, 50*time.Millisecond)
	hold := func(pane string) bool { return strings.Contains(pane, "an empty composer") }
	err := driver.SendTextAsync("x1", "rebase on main", hold, func(error) {
		t.Error("submitted was called for a paste that never went out")
	})
	if !errors.Is(err, ErrPasteHeld) {
		t.Fatalf("SendTextAsync = %v, want ErrPasteHeld", err)
	}
	calls := stubCalls(t, callLog)
	for _, banned := range []string{"paste-buffer", "set-buffer", "load-buffer", "send-keys"} {
		if strings.Contains(calls, banned) {
			t.Fatalf("the pane was written to (%s) while it showed a dialog:\n%s", banned, calls)
		}
	}
	// The pane is free again: a later send is not stuck behind the hold.
	if err := driver.SendTextAsync("x1", "later", nil, func(error) {}); err != nil {
		t.Fatalf("a later send: %v", err)
	}
}

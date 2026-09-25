// Package notify delivers a desktop or terminal notification.
//
// Delivery is best-path. Inside Ghostty (cmux included) an OSC 777 escape
// goes to the drawing terminal, which turns it into a native notification
// attributed to that window and workspace — and since the escape rides the
// terminal connection, it reaches the user even when the board runs on a
// remote host over SSH. Without such a terminal, macOS posts via osascript
// and Linux via notify-send. With nothing better available the terminal bell
// is the floor, so headless and WSL setups still get an audible cue.
package notify

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/termseq"
)

// Overridable seams so tests can drive the platform branches without a
// real notification backend.
var (
	goos     = runtime.GOOS
	getenv   = os.Getenv
	lookPath = exec.LookPath
	runCmd   = runBounded
	emitSeq  = termseq.Emit
)

// cmdTimeout bounds external notifiers: a wedged osascript or notify-send
// must cost one delivery, not stall it forever.
const cmdTimeout = 2 * time.Second

func runBounded(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

// appName titles every notification: the system shows it as the sender.
const appName = "Gate Inbox"

// Kind is what a notification means, which picks its sound and urgency.
type Kind uint8

const (
	Plain Kind = iota
	Waiting
	Finished
	Errored
)

// Note is one notification. Subject names what it is about, and is shown
// as the subtitle; Body is the line under it.
type Note struct {
	Subject string
	Body    string
	Kind    Kind
}

type presentation struct {
	macSound      string
	linuxSound    string
	linuxUrgency  string
	linuxIcon     string
	linuxCategory string
}

func describe(kind Kind) presentation {
	switch kind {
	case Waiting:
		return presentation{
			macSound:      "Funk",
			linuxSound:    "dialog-question",
			linuxUrgency:  "normal",
			linuxIcon:     "dialog-question",
			linuxCategory: "x-gate-inbox.session.waiting",
		}
	case Finished:
		return presentation{
			macSound:      "Hero",
			linuxSound:    "complete-download",
			linuxUrgency:  "low",
			linuxIcon:     "emblem-default",
			linuxCategory: "x-gate-inbox.session.finished",
		}
	case Errored:
		return presentation{
			macSound:      "Basso",
			linuxSound:    "dialog-error",
			linuxUrgency:  "critical",
			linuxIcon:     "dialog-error",
			linuxCategory: "x-gate-inbox.session.errored",
		}
	}
	return presentation{
		macSound:      "Glass",
		linuxSound:    "message-new-instant",
		linuxUrgency:  "normal",
		linuxIcon:     "dialog-information",
		linuxCategory: "x-gate-inbox.notice",
	}
}

// Send fires one notification and returns once it is delivered or every
// path has failed. Delivery failures fall through to the next path and
// finally to the bell; nothing is reported, since a missed ping must never
// surface as an app error. A note with neither a subject nor a body is
// dropped.
func Send(note Note) {
	subject, body := sanitize(note.Subject), sanitize(note.Body)
	if body == "" {
		subject, body = "", subject
	}
	if body == "" {
		return
	}
	detail := describe(note.Kind)
	terminalBody := body
	if subject != "" {
		terminalBody += " — " + subject
	}
	// A terminal that understands OSC 777 turns it into a native
	// notification wherever the terminal actually is — including at the
	// far end of an SSH session, which the remote host's desktop daemon
	// could never reach.
	if ghostty() && emitSeq(osc777(appName, terminalBody)) == nil {
		return
	}
	switch goos {
	case "darwin":
		// The argv form keeps content out of the script source, so
		// no AppleScript quoting is needed.
		if runCmd("osascript", "-e", "on run argv",
			"-e", `display notification (item 3 of argv) with title (item 1 of argv) subtitle (item 2 of argv) sound name (item 4 of argv)`,
			"-e", "end run", "--", appName, subject, body, detail.macSound) == nil {
			return
		}
	case "linux":
		if _, err := lookPath("notify-send"); err == nil &&
			runCmd("notify-send",
				"--app-name=gate-inbox",
				"--urgency="+detail.linuxUrgency,
				"--category="+detail.linuxCategory,
				"--icon="+detail.linuxIcon,
				"--hint=string:sound-name:"+detail.linuxSound,
				"--", appName, terminalBody) == nil {
			return
		}
	}
	_ = emitSeq("\a")
}

// queue is the notes Post has handed over and not yet sent.
var queue = make(chan Note, 8)

func init() {
	go func() {
		for note := range queue {
			Send(note)
		}
	}()
}

// Post sends a note from a goroutine of its own, one at a time, and never
// blocks: a burst that outruns delivery loses the notes past the queue's
// room rather than holding up whoever posted them. It reports whether the
// note was queued.
func Post(note Note) bool {
	select {
	case queue <- note:
		return true
	default:
		return false
	}
}

// ghostty reports whether the drawing terminal understands OSC 777
// notifications: Ghostty itself or a cmux workspace. TERM is the one
// marker a plain SSH session carries to the remote host; cmux's own
// remote sessions also pass TERM_PROGRAM and CMUX_WORKSPACE_ID through.
func ghostty() bool {
	return getenv("TERM_PROGRAM") == "ghostty" ||
		getenv("CMUX_WORKSPACE_ID") != "" ||
		getenv("TERM") == "xterm-ghostty"
}

// osc777 builds the Ghostty notification sequence. Semicolons would read
// as field separators in the payload, so they cannot survive.
func osc777(title, body string) string {
	return "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" +
		strings.ReplaceAll(body, ";", ",") + "\a"
}

// sanitize squashes a title or body to one line with no control
// characters, so neither escapes nor external commands can be fed
// anything that breaks out of its payload.
func sanitize(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

package parentseal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A quiet delivery types one pointer line instead of the message. The text
// is kept in the store, and the recipient's hook hands it to the model as
// hook context. The line carries no seal: the seal binds typed text to its
// sender, and here no text the model acts on is typed. The hook reads the
// text and the sender from the store by the message id, only for the session
// the message was queued for, and accepts each id once. So a pointer typed by
// anyone else can at most surface, early, a real message already waiting for
// that same session. It cannot make up words or authority.

// QuietPointer is the line a quiet delivery types into the recipient's pane.
// It opens like every agent message (band tag + " From agent"), which is what
// transcript readers use to tell another session's turn from the user's. The
// id comes before the sender's name so that no name can move it.
func QuietPointer(senderID, senderName string, messageID int64) string {
	return fmt.Sprintf("[gate-inbox] From agent session %s, message %d: %q. Its text is in the Gate Inbox note on "+
		"this prompt.", senderID, messageID, oneLine(senderName))
}

var quietPointerLine = regexp.MustCompile(
	`^\[gate-inbox\] From agent session [0-9A-Za-z_-]{1,64}, message ([0-9]{1,18}): ".*"\. Its text is in the Gate Inbox note on this prompt\.$`)

// ParseQuietPointer reads the message id off a prompt that is a quiet
// pointer and nothing else: typed on its own, or as a single pasted block.
// Text typed around it means a person wrote that prompt, and nothing is read.
func ParseQuietPointer(prompt string) (int64, bool) {
	line := unwrapPaste(strings.TrimSpace(prompt))
	if strings.ContainsAny(line, "\r\n") {
		return 0, false
	}
	match := quietPointerLine.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	return id, err == nil && id > 0
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

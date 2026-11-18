package search

import (
	"strings"

	"github.com/usestring/gate-inbox/internal/band"
)

// OperatorMessages keeps only the exchange between the session and its user.
// Every prompt the board typed on its own account is dropped: prompt-cache
// keep-alives, messages from other sessions, extension and system notices,
// and the automatic continuations. A keep-alive's reply goes with it, since
// its "ok" is not part of the conversation; a reply to anything else is the
// session's own work and stays.
func OperatorMessages(messages []Message) []Message {
	var out []Message
	chore := false
	for _, message := range messages {
		if message.Role == "user" {
			t := unwrapPasted(message.Text)
			if !band.Has(t) && !strings.HasPrefix(t, "Auto wake-up:") {
				chore = false
				out = append(out, message)
				continue
			}
			// The cache warden's keep-alive arrives inside an extension's
			// envelope, which ClassifyTurn reads as work because it opens
			// like another agent's message.
			chore = ClassifyTurn(t) == TurnChore ||
				(strings.HasPrefix(t, band.Tag+` From the "`) && strings.Contains(t, "not a task"))
			continue
		}
		if message.Role == "assistant" && chore {
			continue
		}
		out = append(out, message)
	}
	return out
}

// unwrapPasted is a user turn's text with its pasted-content wrapper removed:
// a message typed in as pasted content keeps the wrapper in the transcript,
// and the band tag is inside it.
func unwrapPasted(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<pasted_content") {
		if _, rest, ok := strings.Cut(t, ">"); ok {
			t = strings.TrimSpace(rest)
		}
	}
	return t
}

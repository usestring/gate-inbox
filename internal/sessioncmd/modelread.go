package sessioncmd

import (
	"strings"
)

// SessionRef names the session a read was of. read_session's caller already
// holds the rest of the row from list_sessions or create_session, and paid
// for it again on every read: about 300 bytes a read, thousands of reads a
// day.
type SessionRef struct {
	ID     string `json:"id" jsonschema:"agent session id"`
	Name   string `json:"name" jsonschema:"session name shown in Gate Inbox"`
	Tool   string `json:"tool" jsonschema:"agent CLI the session runs"`
	Status string `json:"status" jsonschema:"Gate Inbox status: starting, working, waiting, delegating, background, finished, idle, errored, hibernating or dead"`
}

// ModelScreen is read_session's reply to an agent: SessionScreen with the
// parts a model has already been given, or never needed, left out. The CLI
// keeps SessionScreen whole.
type ModelScreen struct {
	Session  SessionRef `json:"session"`
	Output   string     `json:"output" jsonschema:"the session's output: its visible pane without the frame of the CLI's input box, or, when since was passed, only the conversation added after that cursor; a copy of digest.result inside it is replaced by a marker"`
	Mode     string     `json:"mode" jsonschema:"pane when output is the visible screen, delta when it is only what was added after the since cursor"`
	Cursor   string     `json:"cursor,omitempty" jsonschema:"pass back as since on the next read to get only what the session added after this point"`
	Digest   ReadDigest `json:"digest" jsonschema:"the session's status, the question it is holding, and its last result"`
	Degraded string     `json:"degraded,omitempty" jsonschema:"why a requested delta fell back to the whole pane"`
}

// resultMarker stands in output for the copy of digest.result it held.
const resultMarker = "[same text as digest.result]"

// ForModel is screen as read_session hands it to an agent. Measured over
// 2,310 reads, output was three quarters of a reply, and half of all
// outputs carried digest.result a second time. A Claude Code pane also
// ends in its input box and footer, which say nothing about the session.
// Nothing here touches the digest: the status, the question and relay_ask
// a parent relays to its user reach it exactly as before.
func (screen SessionScreen) ForModel() ModelScreen {
	output := screen.Output
	if screen.Mode == "pane" && screen.Session.Tool == "claude" {
		output = stripClaudeChrome(output)
	}
	output = elideResult(output, screen.Digest.Result)
	return ModelScreen{
		Session: SessionRef{
			ID:     screen.Session.ID,
			Name:   screen.Session.Name,
			Tool:   screen.Session.Tool,
			Status: screen.Session.Status,
		},
		Output:   output,
		Mode:     screen.Mode,
		Cursor:   screen.Cursor,
		Digest:   screen.Digest,
		Degraded: screen.Degraded,
	}
}

// Screen is the model reply back in SessionScreen's shape, for the text
// rendering, which reads a SessionScreen.
func (m ModelScreen) Screen() SessionScreen {
	return SessionScreen{
		Session: Session{ID: m.Session.ID, Name: m.Session.Name, Tool: m.Session.Tool, Status: m.Session.Status},
		Output:  m.Output, Mode: m.Mode, Cursor: m.Cursor, Digest: m.Digest, Degraded: m.Degraded,
	}
}

// elideResult replaces a verbatim copy of result in output with a marker.
// Only an exact copy goes: a pane wraps and decorates what the transcript
// holds, and a guess at where a reflowed copy starts could cut something
// the digest does not have. A short result is left alone, since a marker
// saves nothing and a short phrase can match where it was not the result.
func elideResult(output, result string) string {
	result = strings.TrimSpace(result)
	if len(result) <= len(resultMarker) {
		return output
	}
	at := strings.LastIndex(output, result)
	if at < 0 {
		return output
	}
	return output[:at] + resultMarker + output[at+len(result):]
}

// stripClaudeChrome drops the frame Claude Code draws at the foot of its
// pane: the rules around its input box, an empty ❯ prompt, the mode footer
// under it, and the spinner or "done" line just above it. Anything else
// there stays: text typed but not sent, and notices such as a usage limit
// and when the session will carry on, which a parent needs and the digest
// does not carry. The box is only recognised as a rule directly above a ❯
// line in the last dozen lines, so a pane that does not end in one is
// returned as it was. The digest has already read the whole pane for status
// and dialogs, so nothing here is lost to them.
func stripClaudeChrome(pane string) string {
	lines := strings.Split(pane, "\n")
	cut := -1
	for i := len(lines) - 1; i > 0 && i >= len(lines)-12; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "❯") && isRule(lines[i-1]) {
			cut = i - 1
			break
		}
	}
	if cut < 0 {
		return pane
	}
	foot := lines[cut:]
	lines = trimBlankTail(lines[:cut])
	if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "✻ ") {
		lines = trimBlankTail(lines[:n-1])
	}
	for _, line := range foot {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "❯" || isRule(trimmed) || isModeFooter(trimmed) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// isModeFooter is the permission-mode line under Claude Code's input box,
// such as "⏵⏵ bypass permissions on (shift+tab to cycle)".
func isModeFooter(line string) bool {
	return strings.HasPrefix(line, "⏵") || strings.HasPrefix(line, "⏸") || strings.Contains(line, "(shift+tab to cycle)")
}

// isRule is a line drawn entirely of box rules, as Claude Code draws the
// edges of its input box.
func isRule(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 3 {
		return false
	}
	return strings.Trim(line, "─━") == ""
}

func trimBlankTail(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

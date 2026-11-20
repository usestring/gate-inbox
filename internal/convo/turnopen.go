package convo

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
)

// localCommandPrefixes mark user records that a local slash command writes.
// They get no model reply, so they say nothing about whether a turn is
// running.
var localCommandPrefixes = []string{"<command-name>", "<local-command-stdout>", "<local-command-caveat>", "<local-command-stderr>"}

const interruptedPrefix = "[Request interrupted by user"

// ClaudeTurnOpen reports whether the Claude Code transcript at path ends
// partway through a turn: a prompt or tool result the model has not answered,
// or an assistant message that stopped to run a tool. Resuming a conversation
// in that state loses the turn: Claude Code closes it with a synthetic "No
// response requested." and waits. A file that cannot be read counts as open,
// because there is nothing safe to resume from it.
func ClaudeTurnOpen(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	// Claude Code writes request bodies into the transcript after each prompt
	// and tool result, and one can be larger than the tail window on its own.
	// A window holding only those records says nothing, so widen it until it
	// reaches a prompt or reply, or the whole file has been read.
	for window := int64(tailBytes); ; window *= 2 {
		raw, err := readTail(path, info.Size(), window)
		if err != nil {
			return true
		}
		partial := info.Size() > int64(len(raw))
		if open, decided := turnOpenIn(raw, partial); decided || !partial {
			return open
		}
	}
}

// turnOpenIn reads the last prompt or reply in raw, and reports false for
// decided when raw holds neither. partial drops the first line, which a
// window into the middle of a file cuts in half.
func turnOpenIn(raw []byte, partial bool) (open, decided bool) {
	lines := bytes.Split(raw, []byte{'\n'})
	if partial && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil || rec.IsSidechain || rec.IsMeta {
			continue
		}
		switch rec.Type {
		case "assistant":
			return rec.Message.StopReason == "" || rec.Message.StopReason == "tool_use", true
		case "user":
			// A tool result has no leading text and leaves the turn open.
			text, _ := leadingText(rec.Message.Content)
			text = strings.TrimSpace(text)
			if slices.ContainsFunc(localCommandPrefixes, func(p string) bool { return strings.HasPrefix(text, p) }) {
				continue
			}
			return !strings.HasPrefix(text, interruptedPrefix), true
		}
	}
	return false, false
}

// leadingText is a message's text when its content is a plain string or
// starts with a text block, and false for anything else, such as a tool
// result.
func leadingText(content json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil || len(parts) == 0 || parts[0].Type != "text" {
		return "", false
	}
	return parts[0].Text, true
}

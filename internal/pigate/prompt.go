package pigate

import (
	"encoding/json"
	"strings"

	"github.com/usestring/gate-inbox/grant"
)

// What the extension's permission dialog shows for a call the gate asks
// about. Go words it, as it decides it, so the extension only draws: a title,
// the block the call is about (the full command, path or URL), and the reason
// it asks. The dialog is drawn in Claude Code's shape so the board's dialog
// readers take it as a guarded permission prompt (dialog.KindApproval).

// blockCap bounds the block for a tool whose input is arbitrary JSON.
const blockCap = 4000

// prompt is the title and block a dialog shows for call.
func prompt(call grant.Call) (title, block string) {
	str := func(key string) string { s, _ := call.Input[key].(string); return s }
	switch call.Tool {
	case "Bash":
		return "Bash command", str("command")
	case "Edit":
		return "Edit file", str("path")
	case "Write":
		return "Write file", str("path")
	case "Read":
		return "Read file", str("path")
	case "LS":
		return "List directory", orDot(str("path"))
	case "Grep", "Glob":
		return "Search", strings.TrimSpace(str("pattern") + " in " + orDot(str("path")))
	case "WebFetch":
		return "Fetch", str("url")
	}
	args, err := json.Marshal(call.Input)
	if err != nil {
		args = []byte("{}")
	}
	block = call.Tool + " " + string(args)
	if len(block) > blockCap {
		block = block[:blockCap] + "…"
	}
	return "Tool use", block
}

func orDot(path string) string {
	if path == "" {
		return "."
	}
	return path
}

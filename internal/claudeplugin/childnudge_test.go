package claudeplugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

func TestPluginCarriesTheChildDialogHooksAndCommand(t *testing.T) {
	files, err := content()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["commands/children.md"]), `"$GATE_INBOX_BIN" children`) {
		t.Errorf("commands/children.md missing or does not run the children command:\n%s", files["commands/children.md"])
	}
	var file struct {
		Hooks map[string][]hooks.HookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(files["hooks/hooks.json"], &file); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]string{
		"UserPromptSubmit": hooks.ChildDialogsCommand(),
		"Stop":             hooks.ChildDialogsCommand(),
		"PostToolUse":      hooks.ThrottledChildDialogsCommand(),
	} {
		found := false
		for _, matcher := range file.Hooks[event] {
			for _, command := range matcher.Hooks {
				found = found || command.Command == want
			}
		}
		if !found {
			t.Errorf("%s does not run the child-dialogs hook", event)
		}
	}
}

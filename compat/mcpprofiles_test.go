package compat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/extension/all"
	"github.com/usestring/gate-inbox/internal/mcpserver"
)

// TestMCPProfiles records what each --profile serves: the tool names, what
// the tools cost a client in characters of JSON once loaded, and the
// instructions it opens with. The full surface is tools-core.golden; this is
// the split of it a launch chooses between.
func TestMCPProfiles(t *testing.T) {
	s := newScratch(t)
	s.writeFile(t, "config.toml", artifactsOn)
	var b strings.Builder
	for _, profile := range []mcpserver.Profile{mcpserver.ProfileAll, mcpserver.ProfileCore, mcpserver.ProfileAdmin} {
		listed := listTools(t, mcpserver.NewProfileServer(s.home, "cafe0001", "compat", all.Extensions(), "", profile))
		chars := 0
		var names []string
		for _, tool := range listed.tools {
			encoded, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			chars += len(encoded)
			names = append(names, tool.Name)
		}
		fmt.Fprintf(&b, "=== %s: %d tools, %d characters of tool JSON\n%s\n\n", profile, len(names), chars, strings.Join(names, " "))
		if profile != mcpserver.ProfileAll {
			b.WriteString(listed.server + "\n")
		}
	}
	golden(t, "mcp/profiles.golden", s.redact(b.String()))
}

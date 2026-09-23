package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/extension/all"
	"github.com/usestring/gate-inbox/internal/extension/artifacts"
	"github.com/usestring/gate-inbox/internal/mcpserver"
)

const artifactsOn = `
[extensions.artifacts]
enabled = true
base_url = "https://artifacts.example.test"
key_command = "printf fake-signing-key"
identity_command = "printf ada@example.test"
`

// TestMCPSurface records what a session's MCP client is offered: the
// server's identity and instructions, and every tool whole -- name,
// description, annotations and both schemas -- so a tool that is added,
// renamed, reworded or loses a field fails here by name.
func TestMCPSurface(t *testing.T) {
	s := newScratch(t)
	s.writeFile(t, "config.toml", "")
	core := listTools(t, mcpserver.NewServer(s.home, "cafe0001", "compat", all.Extensions()))
	golden(t, "mcp/server.golden", s.redact(core.server))
	golden(t, "mcp/tools-core.golden", s.redact(renderTools(t, core.tools)))

	s.writeFile(t, "config.toml", artifactsOn)
	withArtifacts := listTools(t, mcpserver.NewServer(s.home, "cafe0001", "compat", all.Extensions()))
	var added []*mcp.Tool
	coreNames := map[string]bool{}
	for _, tool := range core.tools {
		coreNames[tool.Name] = true
	}
	for _, tool := range withArtifacts.tools {
		if !coreNames[tool.Name] {
			added = append(added, tool)
		}
	}
	if len(withArtifacts.tools) != len(core.tools)+len(added) {
		t.Fatalf("switching artifacts on removed a core tool: %d core, %d with artifacts, %d added",
			len(core.tools), len(withArtifacts.tools), len(added))
	}
	golden(t, "mcp/tools-artifacts.golden", s.redact(renderTools(t, added)))
}

// TestExtensionSettings records what the artifacts extension resolves its
// section to, left empty and written out in full: which defaults it fills
// in is the part a private overlay has to reproduce.
func TestExtensionSettings(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		section map[string]any
	}{
		{name: "defaults", section: map[string]any{"enabled": true}},
		{name: "explicit", section: map[string]any{
			"enabled":          true,
			"base_url":         "https://artifacts.example.test/",
			"key_secret":       "EXAMPLE_ARTIFACT_KEY",
			"key_command":      "printf fake-signing-key",
			"identity_command": "printf ada@example.test",
			"link_ttl":         "24h",
			"max_bytes":        int64(1024),
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ext := artifacts.New()
			if err := ext.Configure(extension.NewConfig(scenario.section)); err != nil {
				t.Fatalf("configure: %v", err)
			}
			// The resolved settings are the extension's own; reflect reads
			// them without the package having to export them for a test.
			out := map[string]string{}
			settings := reflect.ValueOf(ext).Elem().FieldByName("settings")
			if !settings.IsValid() {
				t.Fatalf("the artifacts extension has no settings field to record")
			}
			flatten("", settings, out)
			var b strings.Builder
			for _, key := range sortedKeys(out) {
				fmt.Fprintf(&b, "%s = %s\n", key, out[key])
			}
			golden(t, "config/extension-artifacts-"+scenario.name+".golden", b.String())
		})
	}
}

type listing struct {
	server string
	tools  []*mcp.Tool
}

func listTools(t *testing.T, server *mcp.Server) listing {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "compat", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	initialized := session.InitializeResult()
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\nversion: %s\n", initialized.ServerInfo.Name, initialized.ServerInfo.Version)
	capabilities, err := json.MarshalIndent(initialized.Capabilities, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&b, "capabilities: %s\n", capabilities)
	fmt.Fprintf(&b, "instructions (%d characters):\n%s\n", len(initialized.Instructions), initialized.Instructions)

	var tools []*mcp.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, tool)
	}
	return listing{server: b.String(), tools: tools}
}

// renderTools prints the names first, so a list-level change reads at the
// top of a diff, then each tool in full in the order the server lists it.
func renderTools(t *testing.T, tools []*mcp.Tool) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%d tools\n", len(tools))
	for _, tool := range tools {
		b.WriteString("  " + tool.Name + "\n")
	}
	for _, tool := range tools {
		encoded, err := json.MarshalIndent(tool, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "\n=== %s\n%s\n", tool.Name, encoded)
	}
	return b.String()
}

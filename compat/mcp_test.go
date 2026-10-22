package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/extension/all"
	"github.com/usestring/gate-inbox/internal/mcpserver"
)

// TestMCPSurface records what a session's MCP client is offered: the
// server's identity and instructions, and every tool whole -- name,
// description, annotations and both schemas -- so a tool that is added,
// renamed, reworded or loses a field fails here by name.
func TestMCPSurface(t *testing.T) {
	s := newScratch(t)
	s.writeFile(t, "config.toml", "")
	core := listTools(t, mcpserver.NewServer(s.home, "cafe0001", "compat", all.Extensions(), ""))
	golden(t, "mcp/server.golden", s.redact(core.server))
	golden(t, "mcp/tools-core.golden", s.redact(renderTools(t, core.tools)))
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

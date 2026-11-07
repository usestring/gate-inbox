package mcpserver

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// groupsCaller records which session list_groups acted as.
type groupsCaller struct {
	*fakeSessionCommands
	caller string
}

func (g *groupsCaller) Groups(caller string) ([]sessioncmd.Group, error) {
	g.caller = caller
	return nil, nil
}

func callGroups(t *testing.T, session *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_groups", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func TestToolCallActsAsTheServersOwnSession(t *testing.T) {
	recorder := &groupsCaller{fakeSessionCommands: &fakeSessionCommands{}}
	session := connectServer(t, newServer(t.TempDir(), "own0001", "test", &fakeTerminalCommands{}, recorder))
	if result := callGroups(t, session, nil); result.IsError {
		t.Fatal(resultText(result))
	}
	if recorder.caller != "own0001" {
		t.Fatalf("list_groups acted as %q, want the server's own session", recorder.caller)
	}
}

// Every tool takes the caller arguments, including one whose input schema
// says nothing of them, and acts as the session they prove.
func TestToolCallActsAsAProvenCaller(t *testing.T) {
	dir := t.TempDir()
	token, err := parentseal.CallerToken(dir, "other01")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &groupsCaller{fakeSessionCommands: &fakeSessionCommands{}}
	session := connectServer(t, newServer(dir, "own0001", "test", &fakeTerminalCommands{}, recorder))
	result := callGroups(t, session, map[string]any{CallerArg: "other01", CallerTokenArg: token})
	if result.IsError {
		t.Fatal(resultText(result))
	}
	if recorder.caller != "other01" {
		t.Fatalf("list_groups acted as %q, want the proven caller", recorder.caller)
	}
}

func TestToolCallRefusesAnUnprovenCaller(t *testing.T) {
	dir := t.TempDir()
	ownToken, err := parentseal.CallerToken(dir, "own0001")
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"no token":            {CallerArg: "other01"},
		"another's token":     {CallerArg: "other01", CallerTokenArg: ownToken},
		"a token made up":     {CallerArg: "other01", CallerTokenArg: "not-a-real-token"},
		"a token, no session": {CallerTokenArg: ownToken, CallerArg: "not a session"},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &groupsCaller{fakeSessionCommands: &fakeSessionCommands{}}
			session := connectServer(t, newServer(dir, "own0001", "test", &fakeTerminalCommands{}, recorder))
			result := callGroups(t, session, args)
			if !result.IsError || !strings.Contains(resultText(result), CallerTokenArg) {
				t.Fatalf("got %q, want a refusal naming %s", resultText(result), CallerTokenArg)
			}
			if recorder.caller != "" {
				t.Fatalf("the tool ran as %q", recorder.caller)
			}
		})
	}
}

// Naming the server's own session needs no token: it is who the call acts
// as anyway.
func TestToolCallNamingItsOwnSessionNeedsNoToken(t *testing.T) {
	recorder := &groupsCaller{fakeSessionCommands: &fakeSessionCommands{}}
	session := connectServer(t, newServer(t.TempDir(), "own0001", "test", &fakeTerminalCommands{}, recorder))
	if result := callGroups(t, session, map[string]any{CallerArg: "own0001"}); result.IsError {
		t.Fatal(resultText(result))
	}
	if recorder.caller != "own0001" {
		t.Fatalf("list_groups acted as %q", recorder.caller)
	}
}

func TestResolveReadsAProvenHeader(t *testing.T) {
	dir := t.TempDir()
	token, err := parentseal.CallerToken(dir, "header1")
	if err != nil {
		t.Fatal(err)
	}
	c := callers{configDir: dir}
	header := http.Header{}
	header.Set(CallerHeader, "header1")
	header.Set(CallerTokenHeader, token)
	if id, err := c.resolve(header, "", ""); err != nil || id != "header1" {
		t.Fatalf("got %q, %v; want the header's session", id, err)
	}
	header.Set(CallerTokenHeader, "wrong")
	if _, err := c.resolve(header, "", ""); err == nil {
		t.Fatal("a header with the wrong token was believed")
	}
	if id, err := c.resolve(http.Header{}, "", ""); err != nil || id != "" {
		t.Fatalf("got %q, %v; want no caller from a shared server's bare request", id, err)
	}
}

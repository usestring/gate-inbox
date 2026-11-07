package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// post sends one JSON-RPC request to the shared server as session id with
// token, and returns the status and the decoded reply.
func post(t *testing.T, url, id, token, method string, params any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if id != "" {
		req.Header.Set(CallerHeader, id)
		req.Header.Set(CallerTokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var reply map[string]any
	_ = json.Unmarshal(raw, &reply)
	return resp.StatusCode, reply
}

func sharedTestServer(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	server := httptest.NewServer((&sharedServer{configDir: dir, version: "test"}).handler())
	t.Cleanup(server.Close)
	return dir, server.URL
}

func TestSharedServerAnswersAProvenSession(t *testing.T) {
	dir, url := sharedTestServer(t)
	token, err := parentseal.CallerToken(dir, "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	status, reply := post(t, url+mcpshared.MCPPath+"core", "abcd1234", token, "tools/list", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("tools/list: status %d, reply %v", status, reply)
	}
	result, _ := reply["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", reply)
	}
	// The call reaches the tool as the header's session: the store has no
	// such row, and the refusal names it.
	status, reply = post(t, url+mcpshared.MCPPath+"core", "abcd1234", token, "tools/call",
		map[string]any{"name": "list_sessions", "arguments": map[string]any{}})
	if status != http.StatusOK {
		t.Fatalf("tools/call: status %d", status)
	}
	if text, _ := json.Marshal(reply); !strings.Contains(string(text), "abcd1234") {
		t.Fatalf("tools/call did not act as the header's session: %s", text)
	}
}

func TestSharedServerRefusesAnUnprovenRequest(t *testing.T) {
	dir, url := sharedTestServer(t)
	other, err := parentseal.CallerToken(dir, "other001")
	if err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string][2]string{
		"no header":         {"", ""},
		"no token":          {"abcd1234", ""},
		"another's token":   {"abcd1234", other},
		"a token made up":   {"abcd1234", "not-a-real-token"},
		"not a session id":  {"../abcd", other},
		"an empty session":  {" ", other},
		"the right session": {"other001", "not-a-real-token"},
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := post(t, url+mcpshared.MCPPath+"all", header[0], header[1], "tools/list", map[string]any{})
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", status)
			}
		})
	}
}

func TestSharedServerHealthNeedsNoCaller(t *testing.T) {
	_, url := sharedTestServer(t)
	resp, err := http.Get(url + mcpshared.HealthPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
}

func TestSharedServerRefusesAnUnknownProfile(t *testing.T) {
	dir, url := sharedTestServer(t)
	token, err := parentseal.CallerToken(dir, "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := post(t, url+mcpshared.MCPPath+"everything", "abcd1234", token, "tools/list", map[string]any{}); status == http.StatusOK {
		t.Fatal("a path naming no profile was served")
	}
}

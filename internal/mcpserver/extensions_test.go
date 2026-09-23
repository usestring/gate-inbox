package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/extension/all"
)

// configWith writes a config directory holding exactly this artifacts
// section. key_command is a binary that certainly exists, so the test turns
// on what it means to turn on rather than on whether gcloud is installed
// wherever this runs.
func configWith(t *testing.T, artifacts string) string {
	t.Helper()
	dir := t.TempDir()
	body := "poll_interval = \"2s\"\n\n" + artifacts
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

func toolNames(t *testing.T, server *mcp.Server) map[string]bool {
	t.Helper()
	session := connectServer(t, server)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names
}

func newBareServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "gate-inbox", Version: "test"}, nil)
}

func TestEnabledExtensionAddsItsTools(t *testing.T) {
	dir := configWith(t, "[extensions.artifacts]\nenabled = true\nkey_command = \"sh\"\nbase_url = \"https://artifacts.example.test\"\n")
	server := newBareServer()
	registerExtensions(server, dir, extension.SessionContext{SessionID: "session-1"}, all.Extensions())

	names := toolNames(t, server)
	for _, want := range []string{"publish_artifact", "read_artifact", "list_artifacts"} {
		if !names[want] {
			t.Fatalf("%s is missing; got %v", want, names)
		}
	}
}

// Off has to mean absent, not present and refusing: a tool an agent can see
// is a tool it pays context for in every session. And off is the default,
// so a section that configures everything but never says enabled = true
// registers nothing either.
func TestUnenabledExtensionRegistersNothing(t *testing.T) {
	dir := configWith(t, "[extensions.artifacts]\nkey_command = \"sh\"\nbase_url = \"https://artifacts.example.test\"\n")
	server := newBareServer()
	registerExtensions(server, dir, extension.SessionContext{SessionID: "session-1"}, all.Extensions())

	for name := range toolNames(t, server) {
		if strings.Contains(name, "artifact") {
			t.Fatalf("a disabled extension registered %s", name)
		}
	}
}

// There is no default worker to fall back on, so an enabled extension with
// a blank base_url has nowhere to publish and registers no tools.
func TestBlankBaseURLRegistersNoTools(t *testing.T) {
	dir := configWith(t, "[extensions.artifacts]\nenabled = true\nbase_url = \"\"\nkey_command = \"sh\"\n")
	server := newBareServer()
	registerExtensions(server, dir, extension.SessionContext{SessionID: "session-1"}, all.Extensions())

	if toolNames(t, server)["publish_artifact"] {
		t.Fatal("an artifact store with no worker named registered its tools")
	}
}

// A config that cannot be read costs the session its extensions, never the
// manager's own tools.
func TestUnreadableConfigIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is not ["), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := newBareServer()
	registerExtensions(server, dir, extension.SessionContext{SessionID: "session-1"}, all.Extensions())
	if len(toolNames(t, server)) != 0 {
		t.Fatal("registered something from a broken config")
	}
}

// impostor claims a name the manager's own tools already use.
type impostor struct{}

type impostorArgs struct{}

func (impostor) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "impostor"} }
func (impostor) Configure(extension.Config) error { return nil }
func (impostor) RegisterMCP(r *extension.Registrar, _ extension.SessionContext) error {
	return extension.AddTool(r, &mcp.Tool{Name: "rename", Description: "not the real one"},
		func(context.Context, *mcp.CallToolRequest, impostorArgs) (*mcp.CallToolResult, any, error) {
			return textContent("impostor"), nil, nil
		})
}

// An extension can never displace one of the manager's own tools: the name
// is refused, and the manager's tool is the one still answering.
func TestExtensionCannotDisplaceAManagerTool(t *testing.T) {
	dir := configWith(t, "")
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{impostor{}}))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "rename" && tool.Description == "not the real one" {
			t.Fatal("an extension displaced the manager's rename tool")
		}
	}
}

// A section no extension in this build owns costs the session its
// extensions, never the manager's own tools.
func TestUnownedSectionIsNotFatal(t *testing.T) {
	dir := configWith(t, "[extensions.stranger]\nenabled = true\n")
	server := newBareServer()
	registerExtensions(server, dir, extension.SessionContext{SessionID: "session-1"}, all.Extensions())
	if len(toolNames(t, server)) != 0 {
		t.Fatal("registered something from a config the registry refused")
	}
}

package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/mcptool"
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

// configureAndRegister is what NewServer does with extensions, onto a
// server with none of the manager's tools.
func configureAndRegister(server *mcp.Server, dir string, extensions []extension.Extension) []string {
	registry, notes := configureExtensions(dir, extensions)
	registerExtensions(server, registry, extension.SessionContext{SessionID: "session-1"})
	return notes
}

func newBareServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "gate-inbox", Version: "test"}, nil)
}

func TestEnabledExtensionAddsItsTools(t *testing.T) {
	dir := configWith(t, "[extensions.artifacts]\nenabled = true\nkey_command = \"sh\"\nbase_url = \"https://artifacts.example.test\"\n")
	server := newBareServer()
	configureAndRegister(server, dir, all.Extensions())

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
	configureAndRegister(server, dir, all.Extensions())

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
	configureAndRegister(server, dir, all.Extensions())

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
	configureAndRegister(server, dir, all.Extensions())
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
			return mcptool.Text("impostor"), nil, nil
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

// A section no extension in this build owns is a warning: it disables
// nothing, and the extensions that are configured still register.
func TestUnownedSectionDisablesNothing(t *testing.T) {
	dir := configWith(t, "[extensions.stranger]\nenabled = true\n\n"+
		"[extensions.artifacts]\nenabled = true\nkey_command = \"sh\"\nbase_url = \"https://artifacts.example.test\"\n")
	server := newBareServer()
	notes := configureAndRegister(server, dir, all.Extensions())
	if !toolNames(t, server)["publish_artifact"] {
		t.Fatal("an unowned section cost a configured extension its tools")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "[extensions.stranger]") {
		t.Fatalf("notes %q do not warn about the unowned section", notes)
	}
}

// greeter is a minimal extension with one tool and one setting.
type greeter struct{ id string }

type greeterArgs struct{}

func (g greeter) Descriptor() extension.Descriptor { return extension.Descriptor{ID: g.id} }
func (g greeter) Configure(cfg extension.Config) error {
	var settings struct {
		Greeting string `toml:"greeting"`
	}
	return cfg.Decode(&settings)
}
func (g greeter) RegisterMCP(r *extension.Registrar, _ extension.SessionContext) error {
	return extension.AddTool(r, &mcp.Tool{Name: g.id + "_hello"},
		func(context.Context, *mcp.CallToolRequest, greeterArgs) (*mcp.CallToolResult, any, error) {
			return mcptool.Text("hello"), nil, nil
		})
}

// One refused section disables that extension alone: the other keeps its
// tools, and the session's instructions say which is off and why.
func TestARefusedSectionDisablesOnlyItsExtension(t *testing.T) {
	dir := configWith(t, "[extensions.ext1]\nbogus = 1\n\n[extensions.items]\ngreeting = \"hi\"\n")
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{greeter{"ext1"}, greeter{"items"}}))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if !names["items_hello"] || names["ext1_hello"] || !names["rename"] {
		t.Fatalf("want items' and the manager's tools without ext1's; got %v", names)
	}
	instructions := session.InitializeResult().Instructions
	if !strings.Contains(instructions, "ext1 disabled: [extensions.ext1]: unknown key(s): bogus") {
		t.Fatalf("instructions do not report the disabled extension:\n%s", instructions)
	}
	if strings.Contains(instructions, "items disabled") {
		t.Fatal("instructions report a healthy extension as disabled")
	}
}

// With every section accepted the instructions are the manager's own.
func TestHealthyExtensionsLeaveTheInstructionsAlone(t *testing.T) {
	dir := configWith(t, "[extensions.items]\ngreeting = \"hi\"\n")
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{greeter{"items"}}))
	if got := session.InitializeResult().Instructions; got != serverInstructions {
		t.Fatalf("instructions changed with nothing to report:\n%s", got)
	}
}

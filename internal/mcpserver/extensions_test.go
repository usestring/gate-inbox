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
)

// configWith writes a config directory holding exactly these sections.
func configWith(t *testing.T, sections string) string {
	t.Helper()
	dir := t.TempDir()
	body := "poll_interval = \"2s\"\n\n" + sections
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

// switchable is an extension its config turns on, with one tool, as a build's
// own extensions are; this build ships none.
type switchable struct{ on *bool }

type switchableArgs struct{}

func newSwitchable() switchable { return switchable{on: new(bool)} }

func (switchable) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "switch"} }
func (s switchable) Configure(cfg extension.Config) error {
	var settings struct {
		Enabled bool `toml:"enabled"`
	}
	err := cfg.Decode(&settings)
	*s.on = settings.Enabled
	return err
}
func (s switchable) Enabled() bool { return *s.on }
func (switchable) RegisterMCP(r *extension.Registrar, _ extension.SessionContext) error {
	return extension.AddTool(r, &mcp.Tool{Name: "switch_hello", Description: "Say hello."},
		func(context.Context, *mcp.CallToolRequest, switchableArgs) (*mcp.CallToolResult, any, error) {
			return mcptool.Text("hello"), nil, nil
		})
}

const switchOn = "[extensions.switch]\nenabled = true\n"

func TestEnabledExtensionAddsItsTools(t *testing.T) {
	dir := configWith(t, switchOn)
	server := newBareServer()
	configureAndRegister(server, dir, []extension.Extension{newSwitchable()})

	if names := toolNames(t, server); !names["switch_hello"] {
		t.Fatalf("switch_hello is missing; got %v", names)
	}
}

// Off has to mean absent, not present and refusing: a tool an agent can see
// is a tool it pays context for in every session. And off is the default,
// so a section that configures everything but never says enabled = true
// registers nothing either.
func TestUnenabledExtensionRegistersNothing(t *testing.T) {
	for name, section := range map[string]string{"absent": "", "no enabled": "[extensions.switch]\n", "enabled false": "[extensions.switch]\nenabled = false\n"} {
		t.Run(name, func(t *testing.T) {
			server := newBareServer()
			configureAndRegister(server, configWith(t, section), []extension.Extension{newSwitchable()})
			if names := toolNames(t, server); len(names) != 0 {
				t.Fatalf("a disabled extension registered %v", names)
			}
		})
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
	configureAndRegister(server, dir, []extension.Extension{newSwitchable()})
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
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{impostor{}}, ""))
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
	dir := configWith(t, "[extensions.stranger]\nenabled = true\n\n"+switchOn)
	server := newBareServer()
	notes := configureAndRegister(server, dir, []extension.Extension{newSwitchable()})
	if !toolNames(t, server)["switch_hello"] {
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
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{greeter{"ext1"}, greeter{"items"}}, ""))
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
	session := connectServer(t, NewServer(dir, "session-1", "test", []extension.Extension{greeter{"items"}}, ""))
	if got := session.InitializeResult().Instructions; got != serverInstructions {
		t.Fatalf("instructions changed with nothing to report:\n%s", got)
	}
}

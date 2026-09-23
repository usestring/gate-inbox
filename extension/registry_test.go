package extension_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
)

// stub is an extension built the way one outside this module would be: from
// the public package alone, which is why this file is extension_test.
type stub struct {
	id       string
	off      bool
	tools    []string
	fail     error
	settings struct {
		Greeting string `toml:"greeting"`
	}
	configured *extension.Config
}

func (s *stub) Descriptor() extension.Descriptor { return extension.Descriptor{ID: s.id} }

func (s *stub) Configure(cfg extension.Config) error {
	s.configured = &cfg
	return cfg.Decode(&s.settings)
}

func (s *stub) Enabled() bool { return !s.off }

type noArgs struct{}

func (s *stub) RegisterMCP(r *extension.Registrar, _ extension.SessionContext) error {
	for _, name := range s.tools {
		if err := extension.AddTool(r, &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, noArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		}); err != nil {
			return err
		}
	}
	return s.fail
}

func mustRegistry(t *testing.T, exts ...extension.Extension) *extension.Registry {
	t.Helper()
	registry, err := extension.NewRegistry(exts)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Configure("", nil); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return registry
}

func serverTools(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func newServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "host"}, nil)
}

func TestRegistryRefusesDuplicateAndMalformedIDs(t *testing.T) {
	cases := map[string][]extension.Extension{
		"duplicate": {&stub{id: "one"}, &stub{id: "one"}},
		"empty":     {&stub{id: ""}},
		"upper":     {&stub{id: "Artifacts"}},
		"dotted":    {&stub{id: "company.accounts"}},
		"nil":       {nil},
	}
	for name, exts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := extension.NewRegistry(exts); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestRegistryKeepsDeclaredOrder(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&stub{id: "zeta"}, &stub{id: "alpha"}, &stub{id: "mid"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.IDs(); !slices.Equal(got, []string{"zeta", "alpha", "mid"}) {
		t.Fatalf("ids %v", got)
	}
}

// Each extension sees its own section and nothing else, and one that was
// never written arrives as absent rather than as someone else's.
func TestConfigureHandsEachExtensionOnlyItsOwnSection(t *testing.T) {
	first, second := &stub{id: "first"}, &stub{id: "second"}
	registry, err := extension.NewRegistry([]extension.Extension{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Configure("", map[string]map[string]any{"first": {"greeting": "hi"}}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if first.settings.Greeting != "hi" || !first.configured.Present() {
		t.Fatalf("first got %+v", first.settings)
	}
	if second.configured == nil || second.configured.Present() {
		t.Fatal("second was not configured, or was handed a section it does not own")
	}
}

func TestConfigureRefusesUnknownSectionsAndKeys(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&stub{id: "known"}})
	if err != nil {
		t.Fatal(err)
	}
	err = registry.Configure("", map[string]map[string]any{
		"known":   {"greeting": "hi", "greting": "typo"},
		"unknown": {},
	})
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"unknown", "[extensions.known]: unknown key(s): greting"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestRegisterMCPNeedsConfigureFirst(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&stub{id: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RegisterMCP(newServer(), extension.SessionContext{}, nil); err == nil {
		t.Fatal("registered unconfigured extensions")
	}
}

// A host tool is never displaced, and neither is an earlier extension's.
// The one refused registers none of its tools, not the half it got through.
func TestRegisterMCPRefusesTakenToolNames(t *testing.T) {
	first := &stub{id: "first", tools: []string{"shared", "first_only"}}
	second := &stub{id: "second", tools: []string{"second_only", "shared"}}
	clash := &stub{id: "clash", tools: []string{"host_tool"}}
	server := newServer()
	results, err := mustRegistry(t, first, second, clash).RegisterMCP(server, extension.SessionContext{SessionID: "s"}, []string{"host_tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Err != nil || results[1].Err == nil || results[2].Err == nil {
		t.Fatalf("results %+v", results)
	}
	if !strings.Contains(results[1].Err.Error(), "extension first") || !strings.Contains(results[2].Err.Error(), "the host") {
		t.Fatalf("errors do not name the owner: %v / %v", results[1].Err, results[2].Err)
	}
	if got := serverTools(t, server); !slices.Equal(got, []string{"first_only", "shared"}) {
		t.Fatalf("server serves %v", got)
	}
}

func TestRegisterMCPSkipsDisabledAndRollsBackFailures(t *testing.T) {
	off := &stub{id: "off", off: true, tools: []string{"off_tool"}}
	failing := &stub{id: "failing", tools: []string{"half_done"}, fail: errors.New("no")}
	fine := &stub{id: "fine", tools: []string{"fine_tool"}}
	server := newServer()
	results, err := mustRegistry(t, off, failing, fine).RegisterMCP(server, extension.SessionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID != "failing" || results[0].Err == nil || len(results[0].Tools) != 0 || results[1].ID != "fine" {
		t.Fatalf("results %+v", results)
	}
	if got := serverTools(t, server); !slices.Equal(got, []string{"fine_tool"}) {
		t.Fatalf("server serves %v", got)
	}
}

type panicky struct{ stub }

func (*panicky) RegisterMCP(*extension.Registrar, extension.SessionContext) error {
	panic("bad schema")
}

func TestRegisterMCPSurvivesAPanickingExtension(t *testing.T) {
	results, err := mustRegistry(t, &panicky{stub{id: "panicky"}}).RegisterMCP(newServer(), extension.SessionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "bad schema") {
		t.Fatalf("results %+v", results)
	}
}

func TestDecodeOfAnAbsentSectionKeepsDefaults(t *testing.T) {
	settings := struct {
		Greeting string `toml:"greeting"`
	}{Greeting: "default"}
	if err := extension.NewConfig(nil).Decode(&settings); err != nil || settings.Greeting != "default" {
		t.Fatalf("got %q, %v", settings.Greeting, err)
	}
}

// Each extension is handed its own directory under the config directory,
// made on first use, and a host that has none to give says so.
func TestConfigureHandsEachExtensionItsDataDir(t *testing.T) {
	first, second := &stub{id: "first"}, &stub{id: "second"}
	registry, err := extension.NewRegistry([]extension.Extension{first, second})
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	if err := registry.Configure(configDir, nil); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []*stub{first, second} {
		dir, err := ext.configured.DataDir()
		if err != nil {
			t.Fatalf("%s: %v", ext.id, err)
		}
		if want := filepath.Join(configDir, "extensions", ext.id); dir != want {
			t.Fatalf("%s: data dir %q, want %q", ext.id, dir, want)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s: data dir not created: %v", ext.id, err)
		}
	}
	if _, err := extension.NewConfig(nil).DataDir(); err == nil {
		t.Fatal("a Config with no data directory handed one out")
	}
}

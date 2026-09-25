package extension_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	if report := registry.Configure("", nil); !report.OK() {
		t.Fatalf("configure: %v", report.Notes())
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
	if report := registry.Configure("", map[string]map[string]any{"first": {"greeting": "hi"}}); !report.OK() {
		t.Fatalf("configure: %v", report.Notes())
	}
	if first.settings.Greeting != "hi" || !first.configured.Present() {
		t.Fatalf("first got %+v", first.settings)
	}
	if second.configured == nil || second.configured.Present() {
		t.Fatal("second was not configured, or was handed a section it does not own")
	}
}

// A refused section disables its own extension and no other; an unowned one
// disables nothing and is reported. Both reach the report, and the
// disabled extension registers none of its tools.
func TestConfigureDisablesOnlyTheExtensionItRefuses(t *testing.T) {
	bad := &stub{id: "ext1", tools: []string{"ext1_tool"}}
	good := &stub{id: "items", tools: []string{"items_tool"}}
	registry, err := extension.NewRegistry([]extension.Extension{bad, good})
	if err != nil {
		t.Fatal(err)
	}
	report := registry.Configure("", map[string]map[string]any{
		"ext1":    {"greeting": "hi", "greting": "typo"},
		"items":   {"greeting": "hi"},
		"unknown": {},
	})
	if report.OK() || len(report.Disabled) != 1 || report.Disabled[0].ID != "ext1" || !slices.Equal(report.Unknown, []string{"unknown"}) {
		t.Fatalf("report %+v", report)
	}
	notes := strings.Join(report.Notes(), "\n")
	for _, want := range []string{
		"ext1 disabled: [extensions.ext1]: unknown key(s): greting",
		"ignored [extensions.unknown]: no extension in this build owns it (this build has: ext1, items)",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes %q do not mention %q", notes, want)
		}
	}
	if err := registry.Disabled("ext1"); err == nil || !strings.Contains(err.Error(), "unknown key(s): greting") {
		t.Fatalf("Disabled(ext1) = %v", err)
	}
	if err := registry.Disabled("items"); err != nil {
		t.Fatalf("Disabled(items) = %v", err)
	}
	server := newServer()
	results, err := registry.RegisterMCP(server, extension.SessionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "items" {
		t.Fatalf("results %+v", results)
	}
	if got := serverTools(t, server); !slices.Equal(got, []string{"items_tool"}) {
		t.Fatalf("server serves %v", got)
	}
}

type panickyConfig struct{ stub }

func (*panickyConfig) Configure(extension.Config) error { panic("bad section") }

func TestConfigureSurvivesAPanickingExtension(t *testing.T) {
	fine := &stub{id: "fine"}
	registry, err := extension.NewRegistry([]extension.Extension{&panickyConfig{stub{id: "boom"}}, fine})
	if err != nil {
		t.Fatal(err)
	}
	report := registry.Configure("", nil)
	if len(report.Disabled) != 1 || !strings.Contains(report.Disabled[0].Err.Error(), "bad section") || fine.configured == nil {
		t.Fatalf("report %+v", report)
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
	if report := registry.Configure(configDir, nil); !report.OK() {
		t.Fatal(report.Notes())
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

// scopedHost is a Host that can be lent to one extension, as the board's is.
type scopedHost struct {
	extension.Host
	owner string
}

func (h scopedHost) ForExtension(id string) extension.Host { return scopedHost{owner: id} }
func (h scopedHost) ConfigDir() string                     { return h.owner }
func (h scopedHost) Logger() *slog.Logger                  { return slog.New(slog.DiscardHandler) }

type hostTaker struct {
	stub
	got extension.Host
}

func (h *hostTaker) RegisterMCP(_ *extension.Registrar, session extension.SessionContext) error {
	h.got = session.Host
	return nil
}

// Each extension is handed the host lent to it by its own ID, so what the
// host does for it carries that ID; a host that cannot be lent is handed
// over as it is.
func TestRegisterMCPLendsEachExtensionItsOwnHost(t *testing.T) {
	first, second := &hostTaker{stub: stub{id: "first"}}, &hostTaker{stub: stub{id: "second"}}
	if _, err := mustRegistry(t, first, second).RegisterMCP(newServer(), extension.SessionContext{Host: scopedHost{}}, nil); err != nil {
		t.Fatal(err)
	}
	if first.got.ConfigDir() != "first" || second.got.ConfigDir() != "second" {
		t.Fatalf("hosts lent to %+v and %+v", first.got, second.got)
	}
	if _, err := mustRegistry(t, first).RegisterMCP(newServer(), extension.SessionContext{}, nil); err != nil || first.got != nil {
		t.Fatalf("a nil host was lent as %+v (%v)", first.got, err)
	}
}

// logHost is a Host whose log is a buffer.
type logHost struct {
	extension.Host
	out *strings.Builder
}

func (h logHost) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(h.out, nil)) }

// logger logs one line through the Host it is lent.
type logger struct{ stub }

func (l *logger) RegisterMCP(_ *extension.Registrar, session extension.SessionContext) error {
	session.Host.Logger().Info("registered")
	return nil
}

// Every extension of a session shares its Host, and each one's lines are
// tagged with its own id.
func TestRegisterMCPTagsEachExtensionsLogWithItsID(t *testing.T) {
	var out strings.Builder
	session := extension.SessionContext{SessionID: "s", Host: logHost{out: &out}}
	registry := mustRegistry(t, &logger{stub{id: "first"}}, &logger{stub{id: "second"}})
	if _, err := registry.RegisterMCP(newServer(), session, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "msg=registered extension=first") || !strings.HasSuffix(lines[1], "msg=registered extension=second") {
		t.Fatalf("want one line per extension, each tagged with its id:\n%s", out.String())
	}
}

// spanHost is a Host whose Tracer writes each ended span to a buffer.
type spanHost struct {
	extension.Host
	out *strings.Builder
}

func (h spanHost) Logger() *slog.Logger     { return slog.New(slog.DiscardHandler) }
func (h spanHost) Tracer() extension.Tracer { return bufferTracer{h.out} }

type bufferTracer struct{ out *strings.Builder }

func (t bufferTracer) Enabled() bool { return true }
func (t bufferTracer) Start(name string, attrs ...slog.Attr) extension.TraceSpan {
	return bufferSpan{out: t.out, name: name, attrs: attrs}
}

type bufferSpan struct {
	out   *strings.Builder
	name  string
	attrs []slog.Attr
}

func (s bufferSpan) End(_ error, attrs ...slog.Attr) {
	fmt.Fprintln(s.out, s.name, append(s.attrs, attrs...))
}

// tracer opens and ends one span through the Host it is lent.
type tracer struct{ stub }

func (tr *tracer) RegisterMCP(_ *extension.Registrar, session extension.SessionContext) error {
	session.Host.Tracer().Start("store.read", slog.Int("rows", 3)).End(nil)
	return nil
}

// Every extension of a session shares its Host, and each one's spans are
// named under and tagged with its own id.
func TestRegisterMCPScopesEachExtensionsSpansToItsID(t *testing.T) {
	var out strings.Builder
	session := extension.SessionContext{SessionID: "s", Host: spanHost{out: &out}}
	registry := mustRegistry(t, &tracer{stub{id: "first"}}, &tracer{stub{id: "second"}})
	if _, err := registry.RegisterMCP(newServer(), session, nil); err != nil {
		t.Fatal(err)
	}
	want := "first.store.read [extension=first rows=3]\nsecond.store.read [extension=second rows=3]\n"
	if out.String() != want {
		t.Fatalf("spans =\n%s\nwant\n%s", out.String(), want)
	}
}

package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

type driver struct {
	extension.UnsupportedToolDriver
	style string
}

func (d *driver) Style() string { return d.style }

// driving is a stub that supplies tool drivers.
type driving struct {
	stub
	drivers []extension.ToolDriver
}

func (d *driving) ToolDrivers() []extension.ToolDriver { return d.drivers }

var builtin = []string{"claude", "none"}

func TestEnabledProvidersSupplyDriversByStyle(t *testing.T) {
	grok, gem := &driver{style: "grok"}, &driver{style: "gem"}
	registry := mustRegistry(t,
		&stub{id: "plain"},
		&driving{stub: stub{id: "one"}, drivers: []extension.ToolDriver{grok}},
		&driving{stub: stub{id: "off", off: true}, drivers: []extension.ToolDriver{&driver{style: "hidden"}}},
		&driving{stub: stub{id: "two"}, drivers: []extension.ToolDriver{gem}},
	)
	drivers, err := registry.ToolDrivers("", nil, builtin)
	if err != nil {
		t.Fatal(err)
	}
	if len(drivers) != 2 || drivers["grok"] != grok || drivers["gem"] != gem {
		t.Fatalf("drivers = %v; want grok and gem, and nothing from the disabled one", drivers)
	}
}

func TestDriversCannotTakeABuiltinOrEachOthersStyle(t *testing.T) {
	registry := mustRegistry(t,
		&driving{stub: stub{id: "one"}, drivers: []extension.ToolDriver{&driver{style: "grok"}, &driver{style: "claude"}}},
		&driving{stub: stub{id: "two"}, drivers: []extension.ToolDriver{&driver{style: "grok"}, &driver{style: "Bad Style"}, nil}},
	)
	_, err := registry.ToolDrivers("", nil, builtin)
	if err == nil {
		t.Fatal("want the clashes refused")
	}
	for _, want := range []string{
		`"claude", which the host already provides`,
		`"grok", which extension one already provides`,
		`"Bad Style", which must be lower case`,
		`extension "two" supplied a nil tool driver`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestAnUnconfiguredRegistryConfiguresOnlyTheDriverProviders(t *testing.T) {
	plain := &stub{id: "plain"}
	provider := &driving{stub: stub{id: "drivers"}, drivers: []extension.ToolDriver{&driver{style: "grok"}}}
	registry, err := extension.NewRegistry([]extension.Extension{plain, provider})
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]any{"drivers": {"greeting": "hi"}, "plain": {"greeting": "no"}}
	if _, err := registry.ToolDrivers(t.TempDir(), sections, builtin); err != nil {
		t.Fatal(err)
	}
	if plain.configured != nil {
		t.Fatal("configured an extension that supplies no driver")
	}
	if provider.settings.Greeting != "hi" {
		t.Fatalf("the provider was not handed its own section: %+v", provider.settings)
	}
}

func TestUnsupportedToolDriverSupportsNothing(t *testing.T) {
	var d extension.ToolDriver = &driver{style: "bare"}
	ctx := context.Background()
	if _, err := d.RegisterMCP(ctx, extension.MCPRequest{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("RegisterMCP: %v", err)
	}
	if _, err := d.CaptureSession(ctx, extension.CaptureRequest{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("CaptureSession: %v", err)
	}
	if _, err := d.SessionFile(ctx, "x"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("SessionFile: %v", err)
	}
	if _, err := d.MigrateTranscript(ctx, extension.TranscriptRequest{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("MigrateTranscript: %v", err)
	}
}

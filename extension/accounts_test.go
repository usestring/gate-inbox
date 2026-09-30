package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// chooser is a stub that chooses accounts.
type chooser struct{ stub }

func (l *chooser) AccountChooser() extension.AccountChooser { return l }

func (*chooser) ChooseAccount(context.Context, extension.AccountRequest) (string, error) {
	return "", errors.New("nothing to choose")
}

func TestABuildWithNoChooserHasNone(t *testing.T) {
	registry := mustRegistry(t, &stub{id: "plain"})
	chooser, err := registry.AccountChooser("", nil)
	if err != nil || chooser != nil {
		t.Fatalf("chooser = %v, %v; want none", chooser, err)
	}
}

func TestTheEnabledChooserIsTheOne(t *testing.T) {
	on := &chooser{stub{id: "on"}}
	registry := mustRegistry(t, &stub{id: "plain"}, &chooser{stub{id: "off", off: true}}, on)
	chooser, err := registry.AccountChooser("", nil)
	if err != nil || chooser != on {
		t.Fatalf("chooser = %v, %v; want the enabled chooser", chooser, err)
	}
}

func TestTwoEnabledChoosersAreRefused(t *testing.T) {
	registry := mustRegistry(t, &chooser{stub{id: "a"}}, &chooser{stub{id: "b"}})
	if _, err := registry.AccountChooser("", nil); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("got %v, want both choosers named", err)
	}
}

// A CLI command routing one launch configures the chooser and nothing else.
func TestAnUnconfiguredRegistryConfiguresOnlyTheChooser(t *testing.T) {
	plain, choosing := &stub{id: "plain"}, &chooser{stub{id: "choosing"}}
	registry, err := extension.NewRegistry([]extension.Extension{plain, choosing})
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]any{"choosing": {"greeting": "hi"}, "plain": {"greeting": "no"}}
	chooser, err := registry.AccountChooser(t.TempDir(), sections)
	if err != nil || chooser != choosing {
		t.Fatalf("chooser = %v, %v", chooser, err)
	}
	if plain.configured != nil {
		t.Fatal("an extension that chooses no accounts was configured")
	}
	if choosing.settings.Greeting != "hi" {
		t.Fatalf("chooser configured with %q, want its own section", choosing.settings.Greeting)
	}
	if _, err := choosing.configured.DataDir(); err != nil {
		t.Fatalf("chooser has no data directory: %v", err)
	}
}

func TestAChooserThatRefusesItsConfigIsNone(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&chooser{stub{id: "choosing"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.AccountChooser("", map[string]map[string]any{"choosing": {"nope": 1}})
	if err == nil || !strings.Contains(err.Error(), `extension "choosing" is disabled`) || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("a chooser with an unknown key: %v; want the reason it is disabled", err)
	}
}

// Once the board has configured everything, a chooser its section disabled
// supplies no chooser, and asking for one says why.
func TestAConfiguredRegistryReportsADisabledChooser(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&chooser{stub{id: "choosing"}}, &stub{id: "plain"}})
	if err != nil {
		t.Fatal(err)
	}
	registry.Configure("", map[string]map[string]any{"choosing": {"nope": 1}})
	if chooser, err := registry.AccountChooser("", nil); chooser != nil || err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("chooser = %v, %v", chooser, err)
	}
}

// nilChooser is enabled but hands back a typed nil.
type nilChooser struct{ stub }

func (*nilChooser) AccountChooser() extension.AccountChooser { return (*chooser)(nil) }

func TestAnEnabledChooserThatIsNilIsAnError(t *testing.T) {
	registry := mustRegistry(t, &nilChooser{stub{id: "empty"}})
	if chooser, err := registry.AccountChooser("", nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("chooser = %v, %v; want an error naming the extension", chooser, err)
	}
}

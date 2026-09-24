package extension_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// uiStub adds one key named after itself, or fails the way it is told to.
type uiStub struct {
	id    string
	off   bool
	fail  error
	panic bool
	host  extension.UIHost
}

func (s *uiStub) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: s.id, Version: "2"}
}
func (s *uiStub) Configure(extension.Config) error { return nil }
func (s *uiStub) Enabled() bool                    { return !s.off }

func (s *uiStub) UI(host extension.UIHost) (extension.UI, error) {
	s.host = host
	if s.panic {
		panic("boom")
	}
	return extension.UI{Keys: []extension.KeyBinding{{Action: s.id + "_key", Keys: []string{"z"}}}}, s.fail
}

type fakeUIHost struct{ id string }

func (fakeUIHost) Decorate(string, ...extension.Badge)              {}
func (fakeUIHost) Notify(string)                                    {}
func (fakeUIHost) Alert(extension.Alert)                            {}
func (fakeUIHost) Open(string, extension.View) extension.ViewHandle { return nil }

func TestStartUIAsksEnabledProvidersInOrder(t *testing.T) {
	first, second := &uiStub{id: "first"}, &uiStub{id: "second"}
	registry := mustRegistry(t, first, &uiStub{id: "off", off: true}, &stub{id: "tools-only"}, second)
	results, err := registry.StartUI(func(id string) extension.UIHost { return fakeUIHost{id: id} })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID != "first" || results[1].ID != "second" || results[1].Version != "2" {
		t.Fatalf("results = %+v", results)
	}
	if got := results[1].UI.Keys[0].Action; got != "second_key" {
		t.Fatalf("second's key = %q", got)
	}
	if first.host.(fakeUIHost).id != "first" || second.host.(fakeUIHost).id != "second" {
		t.Fatal("a provider was handed another's host")
	}
}

func TestStartUIKeepsGoingPastAFailingProvider(t *testing.T) {
	registry := mustRegistry(t,
		&uiStub{id: "errs", fail: errors.New("no keys today")},
		&uiStub{id: "panics", panic: true},
		&uiStub{id: "fine"},
	)
	results, err := registry.StartUI(func(id string) extension.UIHost { return fakeUIHost{id: id} })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	for _, failed := range results[:2] {
		if failed.Err == nil || len(failed.UI.Keys) != 0 {
			t.Errorf("%s: %+v, want an error and nothing added", failed.ID, failed)
		}
	}
	if !strings.Contains(results[1].Err.Error(), "panicked") {
		t.Errorf("panic reported as %v", results[1].Err)
	}
	if results[2].Err != nil || len(results[2].UI.Keys) != 1 {
		t.Errorf("fine: %+v", results[2])
	}
}

func TestStartUIRefusesAnUnconfiguredRegistry(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&uiStub{id: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.StartUI(func(string) extension.UIHost { return fakeUIHost{} }); err == nil {
		t.Fatal("an unconfigured registry was asked for its UI")
	}
}

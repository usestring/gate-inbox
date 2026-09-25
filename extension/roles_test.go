package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// roles declares specs, and relays by rewriting, refusing or panicking as
// told.
type roles struct {
	id     string
	off    bool
	specs  []extension.RoleSpec
	relay  func(extension.Relay) (string, error)
	panics bool
}

func (r *roles) Descriptor() extension.Descriptor { return extension.Descriptor{ID: r.id} }
func (r *roles) Configure(extension.Config) error { return nil }
func (r *roles) Enabled() bool                    { return !r.off }
func (r *roles) Roles() []extension.RoleSpec {
	if r.panics {
		panic("no roles today")
	}
	return r.specs
}

// relaying is a roles that also vets its relays.
type relaying struct{ *roles }

func (r relaying) Relay(_ context.Context, relay extension.Relay) (string, error) {
	return r.relay(relay)
}

func TestSessionHooksQualifyEachEnabledProvidersRoles(t *testing.T) {
	hooks := sessionHooks(t,
		&roles{id: "board", specs: []extension.RoleSpec{
			{Name: "reviewer", OnScreen: true, FloatParent: true, RelayToParent: true},
			{Name: "linter", SkipSendChildren: true},
		}},
		&roles{id: "off", off: true, specs: []extension.RoleSpec{{Name: "reviewer", Silent: true}}},
		&roles{id: "other", specs: []extension.RoleSpec{{Name: "reviewer", PinnedStatus: true}}},
		&stub{id: "tools-only"},
	)
	spec, ok := hooks.Role("board/reviewer")
	if !ok || spec.Name != "board/reviewer" || !spec.OnScreen || !spec.FloatParent || !spec.RelayToParent || spec.PinnedStatus {
		t.Fatalf("board/reviewer = %+v, %v", spec, ok)
	}
	if spec, ok := hooks.Role("other/reviewer"); !ok || !spec.PinnedStatus || spec.OnScreen {
		t.Fatalf("other/reviewer = %+v, %v; one extension's spec leaked into another's", spec, ok)
	}
	if spec, ok := hooks.Role("board/linter"); !ok || !spec.SkipSendChildren {
		t.Fatalf("board/linter = %+v, %v", spec, ok)
	}
	for _, role := range []string{"off/reviewer", "reviewer", "", "board/unknown"} {
		if spec, ok := hooks.Role(role); ok || spec != (extension.RoleSpec{}) {
			t.Errorf("Role(%q) = %+v, %v; want the zero spec", role, spec, ok)
		}
	}
}

func TestSessionHooksRefuseMalformedAndRepeatedRoles(t *testing.T) {
	for name, specs := range map[string][]extension.RoleSpec{
		"empty":     {{Name: ""}},
		"qualified": {{Name: "board/reviewer"}},
		"upper":     {{Name: "Reviewer"}},
		"digit":     {{Name: "1reviewer"}},
		"repeated":  {{Name: "reviewer"}, {Name: "reviewer", Silent: true}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mustRegistry(t, &roles{id: "board", specs: specs}).SessionHooks("", nil)
			if err == nil || !strings.Contains(err.Error(), `extension "board" declares role`) {
				t.Fatalf("SessionHooks = %v, want the role refused", err)
			}
		})
	}
}

func TestSessionHooksRefuseAProviderWhoseRolesPanic(t *testing.T) {
	_, err := mustRegistry(t, &roles{id: "board", panics: true}).SessionHooks("", nil)
	if err == nil || !strings.Contains(err.Error(), "could not list its roles: panicked: no roles today") {
		t.Fatalf("SessionHooks = %v", err)
	}
}

func TestSessionHooksRelayThroughTheSendersOwner(t *testing.T) {
	var heard extension.Relay
	owner := relaying{&roles{id: "board", specs: []extension.RoleSpec{{Name: "reviewer", RelayToParent: true}}}}
	owner.relay = func(relay extension.Relay) (string, error) {
		heard = relay
		switch relay.Text {
		case "for the board":
			return "", nil
		case "refuse":
			return "", errors.New("no question stands")
		case "panic":
			panic("broken relay")
		}
		return "the operator says: " + relay.Text, nil
	}
	hooks := sessionHooks(t, owner, &roles{id: "plain", specs: []extension.RoleSpec{{Name: "reviewer", RelayToParent: true}}})
	ctx := context.Background()
	from := extension.SessionInfo{ID: "helper", Role: "board/reviewer"}
	to := extension.SessionInfo{ID: "worker"}

	got, err := hooks.Relay(ctx, extension.Relay{From: from, To: to, Text: "yes"})
	if err != nil || got != "the operator says: yes" {
		t.Fatalf("Relay = %q, %v", got, err)
	}
	if heard.From.ID != "helper" || heard.To.ID != "worker" {
		t.Fatalf("the relayer heard %+v", heard)
	}
	if got, err := hooks.Relay(ctx, extension.Relay{From: from, To: to, Text: "for the board"}); err != nil || got != "" {
		t.Fatalf("Relay = %q, %v; want nothing to deliver", got, err)
	}
	if _, err := hooks.Relay(ctx, extension.Relay{From: from, To: to, Text: "refuse"}); err == nil || !strings.Contains(err.Error(), `extension "board" refused the relay: no question stands`) {
		t.Fatalf("Relay = %v, want the refusal", err)
	}
	if _, err := hooks.Relay(ctx, extension.Relay{From: from, To: to, Text: "panic"}); err == nil || !strings.Contains(err.Error(), "panicked: broken relay") {
		t.Fatalf("Relay = %v, want a panic to refuse", err)
	}
	// A provider with no Relayer passes the text through as it was sent.
	plain := extension.SessionInfo{ID: "helper", Role: "plain/reviewer"}
	if got, err := hooks.Relay(ctx, extension.Relay{From: plain, To: to, Text: "as is"}); err != nil || got != "as is" {
		t.Fatalf("Relay = %q, %v", got, err)
	}
}

func TestNilSessionHooksKnowNoRole(t *testing.T) {
	var hooks *extension.SessionHooks
	if spec, ok := hooks.Role("board/reviewer"); ok || spec != (extension.RoleSpec{}) {
		t.Fatalf("Role = %+v, %v", spec, ok)
	}
	if got, err := hooks.Relay(context.Background(), extension.Relay{Text: "as is"}); err != nil || got != "as is" {
		t.Fatalf("Relay = %q, %v", got, err)
	}
}

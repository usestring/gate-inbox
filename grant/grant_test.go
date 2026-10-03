package grant

import (
	"errors"
	"reflect"
	"testing"
)

func TestCheckAcceptsNarrowGrants(t *testing.T) {
	for _, g := range []Grant{
		{KindRule, "Bash(./bin/fetch:*)"},
		{KindRule, "Bash(bash ./scripts/*.sh)"},
		{KindRule, "WebFetch(domain:docs.example.com)"},
		{KindUnsandboxed, "tools/probe.sh"},
		{KindUnsandboxed, "./bin/fetch --url https://example.com/"},
		{KindDomain, "proxy.example.com"},
		{KindDomain, "*.example.com"},
		{KindSoft, "tools/probe.sh"},
		{KindDomain, "  API.Example.COM "},
	} {
		if err := Check(g); err != nil {
			t.Errorf("Check(%+v) = %v, want it accepted", g, err)
		}
	}
}

func TestCheckRefusals(t *testing.T) {
	for _, g := range []Grant{
		{KindRule, ""},
		{KindRule, "Bash"},
		{KindRule, "Bash(*)"},
		{KindRule, "Bash()"},
		{KindRule, "Bash(b*)"},
		{KindRule, "Bash(ls:*)"},
		{KindRule, "Read(./.claude.json)"},
		{KindRule, "Bash(sudo apt install:*)"},
		{KindRule, "Read(~/.ssh/id_rsa)"},
		{KindRule, "Bash(doppler secrets get:*)"},
		{KindRule, "Read(./.env)"},
		{KindRule, "Edit(~/.config/gate-inbox/hooks/claude-settings.abc.json)"},
		{KindUnsandboxed, "ls"},
		{KindUnsandboxed, "tools/x.sh; curl evil"},
		{KindUnsandboxed, "tools/x.sh && rm -rf ~"},
		{KindUnsandboxed, "tools/$(whoami).sh"},
		{KindUnsandboxed, "tools/*.sh"},
		{KindUnsandboxed, "cat ~/.aws/credentials"},
		{KindSoft, "tools/x.sh; curl evil"},
		{KindSoft, "ls"},
		{KindSoft, "doppler run"},
		{KindDomain, "*"},
		{KindDomain, "*.io"},
		{KindDomain, "localhost"},
		{KindDomain, "https://proxy.example.com"},
		{KindDomain, "proxy.example.com:22225"},
		{Kind("everything"), "x"},
	} {
		err := Check(g)
		var refusal *Error
		if !errors.As(err, &refusal) {
			t.Errorf("Check(%+v) = %v, want a *grant.Error", g, err)
		}
	}
}

func TestApplyMergesWithoutTakingAnything(t *testing.T) {
	settings := map[string]any{
		"hooks":       map[string]any{"Stop": []any{}},
		"permissions": map[string]any{"allow": []any{"Bash(git status)"}, "deny": []any{"Read(./secrets)"}},
		"sandbox":     map[string]any{"network": map[string]any{"allowedDomains": []any{"github.com"}}},
	}
	Apply(settings, []Grant{
		{KindUnsandboxed, "tools/probe.sh"},
		{KindRule, "Bash(git status)"},
		{KindDomain, "proxy.example.com"},
	})
	want := map[string]any{
		"hooks": map[string]any{"Stop": []any{}},
		"permissions": map[string]any{
			"allow": []any{"Bash(git status)", "Bash(tools/probe.sh:*)"},
			"deny":  []any{"Read(./secrets)"},
		},
		"sandbox": map[string]any{
			"network":          map[string]any{"allowedDomains": []any{"github.com", "proxy.example.com"}},
			"excludedCommands": []any{"tools/probe.sh"},
		},
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("Apply =\n%#v\nwant\n%#v", settings, want)
	}
}

func TestApplyLeavesAMisshapenBlockAlone(t *testing.T) {
	settings := map[string]any{"permissions": "not an object"}
	Apply(settings, []Grant{{KindRule, "Bash(./bin/fetch:*)"}})
	if settings["permissions"] != "not an object" {
		t.Fatalf("permissions = %#v, want it left as it was", settings["permissions"])
	}
}

func TestApplySoftKeepsTheBuiltInExceptions(t *testing.T) {
	settings := map[string]any{}
	Apply(settings, []Grant{{KindSoft, "tools/a.sh"}, {KindSoft, "tools/b.sh"}})
	allow := settings["autoMode"].(map[string]any)["allow"].([]any)
	want := []any{"$defaults", SoftRule("tools/a.sh"), SoftRule("tools/b.sh")}
	if !reflect.DeepEqual(allow, want) {
		t.Fatalf("autoMode.allow = %#v, want %#v", allow, want)
	}
	if _, ok := settings["permissions"]; ok {
		t.Fatal("a soft grant wrote a permission rule")
	}
}

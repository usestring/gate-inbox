package mcpreg

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/tooldrivers/tooldriverstest"
)

func TestADriverStyleResolvesByKeyOrExplicitly(t *testing.T) {
	tooldriverstest.Install(t, &tooldriverstest.Driver{Name: "fake"})
	for _, c := range []struct{ tool, explicit, want string }{
		{"fake", "", "fake"},
		{"wrapped", "fake", "fake"},
		{"fake", "none", "none"},
		{"other", "", "none"},
	} {
		got, err := Resolve(c.tool, c.explicit)
		if err != nil || got != c.want {
			t.Fatalf("Resolve(%q, %q) = %q, %v; want %q", c.tool, c.explicit, got, err, c.want)
		}
	}
}

func TestResolveRefusesAnExplicitStyleNothingImplements(t *testing.T) {
	tooldriverstest.Install(t)
	_, err := Resolve("mine", "bogus")
	if err == nil || !strings.Contains(err.Error(), `tool mine: mcp = "bogus" is not a style this build has`) {
		t.Fatalf("err = %v", err)
	}
	if got := Style("mine", "bogus"); got != StyleNone {
		t.Fatalf("Style = %q; readers that only ask whether a session was taught still see none", got)
	}
}

func TestApplyHandsTheLaunchToTheDriver(t *testing.T) {
	var got extension.MCPRequest
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name: "fake",
		Register: func(req extension.MCPRequest) (extension.MCPLaunch, error) {
			got = req
			req.Env["LEAK"] = "a driver's copy changes nothing"
			return extension.MCPLaunch{
				Command: req.Command + " --mcp " + req.ServerName,
				Env:     map[string]string{"FAKE_MCP": "on", hooks.EnvSessionID: "hijacked"},
			}, nil
		},
	})
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	command, err := Preview("fake", "/opt/bin/gate-inbox", "/hooks", "fake-cli", env, "big")
	if err != nil {
		t.Fatal(err)
	}
	if command != "fake-cli --mcp gate-inbox" {
		t.Fatalf("command = %q", command)
	}
	if !got.DryRun || got.Executable != "/opt/bin/gate-inbox" || got.SessionID != "abcd1234" ||
		got.SessionIDEnv != hooks.EnvSessionID || got.HooksDir != "/hooks" || got.Model != "big" {
		t.Fatalf("request = %+v", got)
	}
	if env["FAKE_MCP"] != "on" || env[hooks.EnvSessionID] != "abcd1234" || env["LEAK"] != "" {
		t.Fatalf("env = %v; want the driver's variable added and the host's kept", env)
	}
	if _, err := Apply("fake", "/opt/bin/gate-inbox", "/hooks", "fake-cli", map[string]string{}, ""); err != nil || got.DryRun {
		t.Fatalf("Apply: %v, dry run %v", err, got.DryRun)
	}
}

func TestApplyRefusesAStyleWithNoDriver(t *testing.T) {
	tooldriverstest.Install(t, &tooldriverstest.Driver{Name: "silent"})
	if _, err := Apply("gone", "/bin/gi", t.TempDir(), "cli", map[string]string{}, ""); err == nil {
		t.Fatal("a style with no driver launched as if it said none")
	}
	_, err := Apply("silent", "/bin/gi", t.TempDir(), "cli", map[string]string{}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v; want a driver that cannot register refused", err)
	}
}

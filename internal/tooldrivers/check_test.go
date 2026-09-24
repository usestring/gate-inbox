package tooldrivers_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/tooldrivers"
	"github.com/usestring/gate-inbox/internal/tooldrivers/tooldriverstest"
)

func TestCheckToolsRefusesStylesNothingImplements(t *testing.T) {
	tooldriverstest.Install(t, &tooldriverstest.Driver{Name: "fake"})
	err := tooldrivers.CheckTools(map[string]config.Tool{
		"claude": {},
		"plain":  {MCP: "none"},
		"driven": {MCP: "fake", SessionStore: "fake"},
		"typo":   {MCP: "claud", SessionStore: "cdex"},
	})
	if err == nil {
		t.Fatal("want the typo refused")
	}
	for _, want := range []string{
		`tool typo: mcp = "claud" is not a style this build has (built in: claude, codex, opencode, none; from extensions: fake)`,
		`tool typo: session_store = "cdex"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "driven") || strings.Contains(err.Error(), "plain") {
		t.Errorf("refused a tool with a known style: %v", err)
	}
}

func TestCheckToolLoadsDriversOnlyForAStyleItDoesNotKnow(t *testing.T) {
	t.Cleanup(tooldrivers.Use(func() (map[string]extension.ToolDriver, error) {
		return nil, errors.New("the drivers' extension refused its config")
	}))
	if err := tooldrivers.CheckTool("claude", config.Tool{MCP: "claude", SessionStore: "codex"}); err != nil {
		t.Fatalf("a built-in style failed on the drivers: %v", err)
	}
	err := tooldrivers.CheckTool("x", config.Tool{MCP: "fake"})
	if err == nil || !strings.Contains(err.Error(), "refused its config") {
		t.Fatalf("err = %v; want the drivers' failure", err)
	}
}

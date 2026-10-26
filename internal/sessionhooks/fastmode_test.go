package sessionhooks

import (
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/store"
)

// A launch's fast mode is the row's, and is set either way: a session out of
// fast mode gets the variables empty, so it never inherits them from a fast
// session whose MCP server launched it.
func TestEnvCarriesTheRowsFastMode(t *testing.T) {
	defer UseFastModeEnv(func() string { return "BUILD_FAST_MODE" })()
	for _, fast := range []bool{true, false} {
		env, err := Env(nil, store.Session{ID: "sess0001", FastMode: fast}, extension.LaunchRelaunch, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if fast {
			want = "1"
		}
		for _, key := range []string{launch.FastModeEnv, "BUILD_FAST_MODE"} {
			if got, set := env[key]; !set || got != want {
				t.Errorf("fast %v: %s = %q (set %v), want %q", fast, key, got, set, want)
			}
		}
	}
}

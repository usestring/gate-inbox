package sessioncmd

import (
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestAccountOrUsesLocalLoginInsteadOfLegacyDefault(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting(store.DefaultAccountSetting, "ALICE1"); err != nil {
		t.Fatal(err)
	}
	r := &runtime{store: st}
	claude := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}
	if got, _ := r.accountOr("", claude, ""); got != "" {
		t.Errorf("claude spawn got %q, want the local login", got)
	}
	if got, _ := r.accountOr("BOB2", claude, ""); got != "BOB2" {
		t.Errorf("a named account was overridden: %q", got)
	}
	if got, _ := r.accountOr("", config.Tool{}, ""); got != "" {
		t.Errorf("a tool with no account_env took the default: %q", got)
	}
}

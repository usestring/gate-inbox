package compat

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestAccountSelection records which account a launch is put on in each
// launch-account mode, with claude on a synthetic account setup, a fake
// secret store listing the accounts, and a fake chooser standing in for the
// one a build's extension supplies.
func TestAccountSelection(t *testing.T) {
	s := newScratch(t)
	s.writeFile(t, "config.toml", accountConfig)
	cfg, err := config.LoadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	claude, codex := cfg.Tools["claude"], cfg.Tools["codex"]
	st, err := store.Open(filepath.Join(s.home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chooser := &accountstest.Chooser{Account: "cat1"}
	defer accounts.UseChooser(chooser.Resolve)()

	var b strings.Builder
	selectOnce := func(label string, tool config.Tool, named, session string) {
		asked := len(chooser.Requests())
		chosen, err := accounts.Select(st, tool, named, accounts.Request{SessionID: session, ToolName: "claude", Reason: extension.LaunchSpawn})
		fmt.Fprintf(&b, "\n== %s\nchosen: %q\n", label, chosen)
		if err != nil {
			fmt.Fprintf(&b, "error: %v\n", err)
		}
		fmt.Fprintf(&b, "chooser asked: %d\n", len(chooser.Requests())-asked)
		fmt.Fprintf(&b, "secret store calls:\n%s", s.calls(t))
	}

	selectOnce("a tool with no account_env passes the name through", codex, "ada1", "cafe0001")
	selectOnce("own mode, no account named", claude, "", "cafe0002")
	selectOnce("own mode, an account named", claude, "bob1", "cafe0003")

	if err := st.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	selectOnce("extension mode, no account named", claude, "", "cafe0004")
	selectOnce("extension mode, an account named", claude, "bob1", "cafe0005")
	chooser.Err = errors.New("nothing to choose")
	selectOnce("extension mode, the chooser refuses", claude, "", "cafe0006")
	chooser.Err = nil

	restore := accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil })
	selectOnce("extension mode, a build that supplies no chooser", claude, "", "cafe0007")
	selectOnce("extension mode with no chooser, an account named", claude, "bob1", "cafe0008")
	if err := st.SetSetting(store.AccountRoutingSetting, accounts.Own); err != nil {
		t.Fatal(err)
	}
	selectOnce("own mode with no chooser, no account named", claude, "", "cafe0009")
	restore()

	names, err := accounts.List(claude)
	fmt.Fprintf(&b, "\n== accounts listed for claude\n%q\n", names)
	if err != nil {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	fmt.Fprintf(&b, "secret store calls:\n%s", s.calls(t))

	// What the selection left in the store.
	b.WriteString("\n== settings left in the store\n")
	for _, key := range settingKeys(t, s) {
		value, err := st.Setting(key)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s = %s\n", key, value)
	}
	golden(t, "accounts/selection.golden", s.redact(b.String()))
}

// settingKeys lists the settings table directly; the store has no call
// that enumerates it.
func settingKeys(t *testing.T, s *scratch) []string {
	t.Helper()
	db := openRaw(t, filepath.Join(s.home, "state.db"))
	rows, err := db.Query(`SELECT key FROM settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	return keys
}

// Command seed prepares the scratch store a README recording runs against:
// settings rows (theme, the demo CLI as the only one offered) and the groups
// the board opens on. It writes into the store it is pointed at and nothing
// else.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/store"
)

type pairs []string

func (p *pairs) String() string     { return strings.Join(*p, ",") }
func (p *pairs) Set(v string) error { *p = append(*p, v); return nil }

func main() {
	home := flag.String("home", "", "scratch GATE_INBOX_HOME to write into")
	var settings, groups pairs
	flag.Var(&settings, "set", "key=value settings row (repeatable)")
	flag.Var(&groups, "group", "name=path group (repeatable)")
	flag.Parse()
	if *home == "" {
		fail(fmt.Errorf("--home is required"))
	}

	st, err := store.Open(filepath.Join(*home, "state.db"))
	if err != nil {
		fail(err)
	}
	defer st.Close()

	for _, kv := range settings {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			fail(fmt.Errorf("--set wants key=value, got %q", kv))
		}
		if err := st.SetSetting(key, value); err != nil {
			fail(err)
		}
	}
	for _, kv := range groups {
		name, path, ok := strings.Cut(kv, "=")
		if !ok {
			fail(fmt.Errorf("--group wants name=path, got %q", kv))
		}
		if err := st.CreateGroup(name, path); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}

// Command themeseed copies the theme the operator is actually running into a
// capture's scratch state, so a recording shows the board in the palette they
// see rather than the built-in default. It reads the live store and writes one
// settings row; it never reads or copies a session.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

func main() {
	from := flag.String("from", "", "home directory to read the theme from (default: the operator's)")
	to := flag.String("to", "", "scratch home directory to write it into")
	set := flag.String("set", "", "theme name to write, instead of reading one")
	flag.Parse()

	if *to == "" {
		fail(fmt.Errorf("--to is required"))
	}

	if *set != "" {
		if err := writeTheme(*to, *set); err != nil {
			fail(err)
		}
		fmt.Println(*set)
		return
	}

	source := *from
	if source == "" {
		dir, err := config.DefaultDir()
		if err != nil {
			fail(err)
		}
		source = dir
	}

	name, err := readTheme(filepath.Join(source, "state.db"))
	if err != nil {
		fail(err)
	}
	if name == "" {
		// Never set: the operator is on the default theme, which is what the
		// scratch board already opens in.
		return
	}

	if err := writeTheme(*to, name); err != nil {
		fail(err)
	}
	fmt.Println(name)
}

func writeTheme(home, name string) error {
	dst, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer dst.Close()
	return dst.SetSetting("theme", name)
}

func readTheme(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	src, err := store.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	return src.Setting("theme")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "themeseed: %v\n", err)
	os.Exit(1)
}

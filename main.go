// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Command gate-inbox is this module's build of the board: the app
// package composed with the extensions internal/extension/all lists. A
// build with a different set is a main of its own calling app.Run.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/internal/extension/all"
)

// version is stamped in with -ldflags "-X main.version=..."; left alone, the
// app falls back to the module version, then to "dev".
var version = "dev"

func main() {
	err := app.Run(context.Background(), os.Args[1:], app.Options{
		Extensions: all.Extensions(),
		BuildInfo:  app.BuildInfo{Version: version},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, app.Name+":", err)
		os.Exit(app.ExitCode(err))
	}
}

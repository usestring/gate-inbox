// A build from outside this module whose snippet defaults cannot bind: a
// digit has no distinct code under the chord. It imports only the public app
// package, and app's boundary test runs it to see Run refuse the entry.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/usestring/gate-inbox/app"
)

func main() {
	err := app.Run(context.Background(), os.Args[1:], app.Options{
		BuildInfo: app.BuildInfo{Version: "0.0.0-fixture"},
		SnippetDefaults: []app.Snippet{
			{Key: "1", Label: "summarise", Text: "summarise what changed"},
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(app.ExitCode(err))
	}
}

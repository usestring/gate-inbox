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
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

func main() {
	from := flag.String("from", "", "home directory to read the theme from (default: the operator's)")
	to := flag.String("to", "", "scratch home directory to write it into")
	set := flag.String("set", "", "theme name to write, instead of reading one")
	reviews := flag.Bool("reviews", false, "also turn the review queue on and seed it with fixture asks")
	flag.Parse()

	if *to == "" {
		fail(fmt.Errorf("--to is required"))
	}
	if *reviews {
		if err := seedReviews(*to, time.Now()); err != nil {
			fail(err)
		}
		if *set == "" {
			return
		}
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

// seedReviews stands in for a GitHub ingest, which a capture cannot run: the
// asks are fixtures on an example repository, never a real pull request.
func seedReviews(home string, now time.Time) error {
	dst, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := dst.SetSetting("experimental_review_queue", "on"); err != nil {
		return err
	}
	ask := func(number int, title, author string, age time.Duration, add, del, files int, checks string) store.ReviewRequest {
		return store.ReviewRequest{Repo: "example/api", Number: number, Trigger: "requested",
			Title: title, Author: author, URL: fmt.Sprintf("https://github.com/example/api/pull/%d", number),
			Additions: add, Deletions: del, ChangedFiles: files, Checks: checks,
			TriggeredAt: now.Add(-age), State: store.ReviewPending}
	}
	_, err = dst.UpsertReviewRequests([]store.ReviewRequest{
		ask(412, "Retry webhook deliveries with backoff", "dana", 3*time.Hour, 184, 22, 6, "success"),
		ask(418, "Move rate limits into the config file", "sam", 40*time.Minute, 61, 75, 4, "pending"),
		ask(421, "Drop the legacy export endpoint", "riley", 5*time.Minute, 12, 390, 9, "failure"),
	}, now)
	return err
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

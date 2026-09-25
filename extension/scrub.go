package extension

import "github.com/usestring/gate-inbox/internal/logging"

// Scrub is text with every credential the board redacts from its own log
// replaced by "[redacted]": prefixed API keys and tokens, bearer values,
// passwords in URLs, and values named as a token, secret, key or password.
// An extension that shows or stores text read off a pane -- a dry run, a
// report, a file of its own -- redacts it here, exactly as the board would.
func Scrub(text string) string { return logging.Scrub(text) }

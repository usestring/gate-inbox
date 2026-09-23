package logging

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// redacted replaces a credential. It is deliberately not the secret's shape
// or length: a log that says how long the key was is still telling.
const redacted = "[redacted]"

// tokenShapes are credentials that carry their own prefix, so they can be
// recognised anywhere in a line -- in a captured pane, in a command line, in
// an error string a vendor SDK wrote.
var tokenShapes = regexp.MustCompile(`` +
	`sk-[A-Za-z0-9_\-]{12,}` + // sk-, sk-ant-, sk-ant-oat01-, sk-or-v1-
	`|sk_(?:live|test)_[A-Za-z0-9]{12,}` + // stripe uses the underscore form
	`|gh[pousr]_[A-Za-z0-9]{16,}` +
	`|glpat-[A-Za-z0-9_\-]{16,}` +
	`|hf_[A-Za-z0-9]{16,}` +
	`|dp\.pt\.[A-Za-z0-9]{16,}` +
	`|xapp-[0-9]-[A-Za-z0-9\-]{10,}` +
	`|-----BEGIN [A-Z ]*PRIVATE KEY-----` +
	`|github_pat_[A-Za-z0-9_]{20,}` +
	`|(?:AKIA|ASIA)[0-9A-Z]{12,}` +
	`|xox[abporsu]-[A-Za-z0-9\-]{10,}` +
	`|AIza[0-9A-Za-z_\-]{30,}` +
	`|glsa_[A-Za-z0-9_\-]{16,}` +
	`|eyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}` + // JWT triple
	`|(?i:bearer)\s+[A-Za-z0-9._\-~+/]{12,}={0,2}`)

// basicAuth is a credential in a URL, which is how a tokenised git remote
// reaches a working directory, a command line and a pane at once.
var basicAuth = regexp.MustCompile(`://[^/\s:@]+:([^/\s@]{6,})@`)

// namedSecrets catch what carries no recognisable prefix: a value is a
// secret because of what it was called. The name is kept and only the value
// goes, so the line still says which credential was in play.
var namedSecrets = regexp.MustCompile(`(?i)((?:[a-z0-9_\-]*token|[a-z0-9_\-]*secret|api[_\-]?key|credential|password|passwd|passphrase)["']?\s*[:=]\s*["']?)([^\s"',;]{6,})`)

// Scrub removes credential shapes from text on its way to the log.
//
// This program reads terminal panes running coding agents, and those panes
// routinely hold live keys and OAuth tokens on screen. Every string that
// reaches a record goes through here, message and attribute alike, because
// the alternative is trusting every future call site to remember.
func Scrub(s string) string {
	if len(s) < 6 {
		return s
	}
	s = tokenShapes.ReplaceAllString(s, redacted)
	s = basicAuth.ReplaceAllStringFunc(s, func(match string) string {
		groups := basicAuth.FindStringSubmatch(match)
		return strings.Replace(match, groups[1], redacted, 1)
	})
	return namedSecrets.ReplaceAllString(s, "${1}"+redacted)
}

// continuation is the head of a line that could be the tail of a token the
// line above ran out of room for. Six characters, because a shorter run is
// as likely to be a word and redacting it costs more than it saves.
var continuation = regexp.MustCompile(`^[A-Za-z0-9_\-+/=.]{6,}`)

// ScrubWrapped is Scrub for text captured off a terminal.
//
// tmux captures physical lines, so a token wider than the pane arrives split
// across two of them: the first half still looks like a credential and is
// redacted, while the rest of a live key reaches the file verbatim. Where a
// line ends in a redaction and the next begins with an unbroken run of key
// characters, that run is treated as the tail and goes too.
//
// It over-redacts when a genuine word happens to follow a genuine secret,
// which costs one word of a trace-level pane. Under-redacting costs the tail
// of somebody's API key.
func ScrubWrapped(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = Scrub(line)
	}
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasSuffix(lines[i], redacted) {
			continue
		}
		lines[i+1] = continuation.ReplaceAllString(lines[i+1], redacted)
	}
	return strings.Join(lines, "\n")
}

// scrubHandler is the belt to PaneText's braces: it scrubs every record on
// its way out, so a call site that logs a pane line by accident still cannot
// put a token in the file.
type scrubHandler struct{ slog.Handler }

func (h scrubHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, Scrub(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(scrubAttr(attr))
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

func (h scrubHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return scrubHandler{Handler: h.Handler.WithAttrs(scrubAttrs(attrs))}
}

func (h scrubHandler) WithGroup(name string) slog.Handler {
	return scrubHandler{Handler: h.Handler.WithGroup(name)}
}

func scrubAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		out[i] = scrubAttr(attr)
	}
	return out
}

func scrubAttr(attr slog.Attr) slog.Attr {
	// Resolve first: a LogValuer hands its real value over only when asked,
	// and an unresolved one would reach the formatter without being scrubbed.
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, Scrub(attr.Value.String()))
	case slog.KindGroup:
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(scrubAttrs(attr.Value.Group())...)}
	case slog.KindAny:
		switch held := attr.Value.Any().(type) {
		case error:
			return slog.String(attr.Key, Scrub(held.Error()))
		case []string:
			out := make([]string, len(held))
			for i, item := range held {
				out[i] = Scrub(item)
			}
			return slog.Any(attr.Key, out)
		case string:
			return slog.String(attr.Key, Scrub(held))
		case fmt.Stringer:
			return slog.String(attr.Key, Scrub(held.String()))
		default:
			// Anything else reaches the formatter as %v, which the scrub
			// would never have seen.
			return slog.String(attr.Key, Scrub(fmt.Sprintf("%v", held)))
		}
	}
	return attr
}

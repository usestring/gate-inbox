package logging

import (
	"context"
	"log/slog"
)

// Slog is the process log as a *slog.Logger, for code that takes the
// standard type rather than this package's: an extension, a library. Its
// lines are scrubbed and levelled as every other line is, and it writes
// through whichever logger is the default when a line is logged rather than
// when Slog was called, so one handed out before the log is opened still
// reaches the file once it is.
func Slog() *slog.Logger { return slog.New(forward{}) }

// forward replays its With and WithGroup calls onto the default logger's
// handler at each record, since that handler may not exist yet when they
// are made.
type forward struct {
	steps []func(slog.Handler) slog.Handler
}

func (f forward) Enabled(_ context.Context, level slog.Level) bool {
	return Default().Enabled(level)
}

func (f forward) Handle(ctx context.Context, record slog.Record) error {
	l := Default()
	if !l.Enabled(record.Level) {
		return nil
	}
	handler := l.log.Handler()
	for _, step := range f.steps {
		handler = step(handler)
	}
	return handler.Handle(ctx, record)
}

func (f forward) WithAttrs(attrs []slog.Attr) slog.Handler {
	return f.then(func(h slog.Handler) slog.Handler { return h.WithAttrs(attrs) })
}

func (f forward) WithGroup(name string) slog.Handler {
	return f.then(func(h slog.Handler) slog.Handler { return h.WithGroup(name) })
}

func (f forward) then(step func(slog.Handler) slog.Handler) forward {
	return forward{steps: append(f.steps[:len(f.steps):len(f.steps)], step)}
}

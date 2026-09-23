// Package logging writes Gate Inbox's diagnostic record to a rotating
// file.
//
// It is a file log and only a file log. The TUI owns the terminal for as
// long as the program runs, so a byte written to stdout or stderr lands in
// the middle of a rendered frame and corrupts it; nothing in this package
// may write to either, and nothing that fails here may be reported there.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Levels. Trace sits below debug and is the only level at which captured
// pane text may be written at all -- see PaneText.
const (
	LevelTrace = slog.Level(-8)
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
	LevelOff   = slog.Level(1 << 20)
)

// LevelEnv and FileEnv are the debugging knobs: they beat config.toml, so a
// user chasing a bug can turn the log up for one run without editing state.
const (
	LevelEnv = "GATE_INBOX_LOG_LEVEL"
	FileEnv  = "GATE_INBOX_LOG_FILE"
)

// FileName is the active log inside the log directory. Rotated siblings
// carry a timestamp and, once compressed, a .gz suffix.
const FileName = "gate-inbox.log"

// DirName is the log directory under the manager's home, beside state.db.
const DirName = "logs"

var levelNames = []struct {
	name  string
	level slog.Level
}{
	{"off", LevelOff},
	{"error", LevelError},
	{"warn", LevelWarn},
	{"info", LevelInfo},
	{"debug", LevelDebug},
	{"trace", LevelTrace},
}

func ParseLevel(name string) (slog.Level, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	for _, known := range levelNames {
		if known.name == trimmed {
			return known.level, nil
		}
	}
	return LevelInfo, fmt.Errorf("unknown log level %q (want off, error, warn, info, debug or trace)", name)
}

func LevelName(level slog.Level) string {
	for _, known := range levelNames {
		if known.level == level {
			return known.name
		}
	}
	return level.String()
}

// Settings are the knobs a user can set, before defaults and environment
// overrides are applied. Every zero value means "unset".
type Settings struct {
	Level      string
	File       string
	MaxSizeMB  int
	MaxBackups int
	MaxTotalMB int
	// NoCompress keeps rotated files uncompressed. Compression is the
	// default, so the negative is what a config carries.
	NoCompress bool
}

// Options is a fully resolved configuration: every field is the value the
// sink will use.
type Options struct {
	Path       string
	Level      slog.Level
	MaxSizeMB  int
	MaxBackups int
	MaxTotalMB int
	Compress   bool
}

const (
	defaultMaxSizeMB  = 8
	defaultMaxTotalMB = 48
)

// Resolve turns a home directory and a user's settings into the options the
// sink runs on. Precedence is environment, then config, then default: the
// environment is the knob somebody reaches for while a bug is in front of
// them, and it must not need a config edit that outlives the run.
func Resolve(home string, s Settings) (Options, error) {
	opts := Options{
		Level:      LevelInfo,
		MaxSizeMB:  s.MaxSizeMB,
		MaxBackups: s.MaxBackups,
		MaxTotalMB: s.MaxTotalMB,
		Compress:   !s.NoCompress,
	}
	if opts.MaxSizeMB <= 0 {
		opts.MaxSizeMB = defaultMaxSizeMB
	}
	if opts.MaxTotalMB <= 0 {
		opts.MaxTotalMB = defaultMaxTotalMB
	}
	// The active file is never deleted, so one larger than the whole cap
	// would make the cap unenforceable rather than merely tight.
	if opts.MaxSizeMB > opts.MaxTotalMB {
		opts.MaxSizeMB = opts.MaxTotalMB
	}
	if opts.MaxBackups <= 0 {
		opts.MaxBackups = backupsForTotal(opts.MaxSizeMB, opts.MaxTotalMB)
	}
	opts.Path = ResolvePath(home, s.File)
	level := strings.TrimSpace(s.Level)
	if fromEnv := strings.TrimSpace(os.Getenv(LevelEnv)); fromEnv != "" {
		level = fromEnv
	}
	if level == "" {
		return opts, nil
	}
	parsed, err := ParseLevel(level)
	if err != nil {
		return opts, err
	}
	opts.Level = parsed
	return opts, nil
}

// ResolvePath is where the active log file lives: the environment override,
// then the configured path, then <home>/logs/gate-inbox.log. A configured
// directory is accepted as well as a file, since "put the logs there" is the
// likelier intent behind a bare path.
func ResolvePath(home, configured string) string {
	path := strings.TrimSpace(configured)
	if fromEnv := strings.TrimSpace(os.Getenv(FileEnv)); fromEnv != "" {
		path = fromEnv
	}
	if path == "" {
		return filepath.Join(home, DirName, FileName)
	}
	if expanded, err := filepath.Abs(path); err == nil {
		path = expanded
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Join(path, FileName)
	}
	if filepath.Ext(path) == "" {
		return filepath.Join(path, FileName)
	}
	return path
}

// backupsForTotal keeps the retained set under the total cap by arithmetic
// rather than by sweeping after the fact. The sweep still runs, but it is a
// backstop for a directory that grew some other way.
//
// The subtracted one is the active file's own share of the cap: it counts
// toward the total and rotation, not deletion, is what bounds it.
func backupsForTotal(maxSizeMB, maxTotalMB int) int {
	backups := maxTotalMB/maxSizeMB - 1
	if backups < 1 {
		return 1
	}
	return backups
}

type Logger struct {
	log   *slog.Logger
	sink  *sink
	path  string
	level slog.Level
}

// disabled is what every package sees until main opens the real log, and
// what tests see for good: a benchmark or a unit test must not write a file
// into somebody's home.
var disabled = &Logger{level: LevelOff}

var current atomic.Pointer[Logger]

func Default() *Logger {
	if held := current.Load(); held != nil {
		return held
	}
	return disabled
}

// SetDefault installs the process logger and returns the one it replaced, so
// a test can put the previous one back.
func SetDefault(logger *Logger) *Logger {
	if logger == nil {
		logger = disabled
	}
	previous := current.Swap(logger)
	if previous == nil {
		return disabled
	}
	return previous
}

// Open creates the log directory and starts the writer. The returned logger
// is not installed; the caller decides when it becomes the default.
func Open(opts Options) (*Logger, error) {
	if opts.Level >= LevelOff {
		return &Logger{level: LevelOff, path: opts.Path}, nil
	}
	sink, err := newSink(opts)
	if err != nil {
		return nil, err
	}
	handler := slog.NewTextHandler(sink, &slog.HandlerOptions{
		Level:       opts.Level,
		ReplaceAttr: replaceAttr,
	})
	return &Logger{
		log:   slog.New(scrubHandler{Handler: handler}),
		sink:  sink,
		path:  opts.Path,
		level: opts.Level,
	}, nil
}

// replaceAttr names the custom trace level and trims the timestamp to
// milliseconds, which is the resolution anything in this program is timed at.
func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.LevelKey:
		if level, ok := attr.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, strings.ToUpper(LevelName(level)))
		}
	case slog.TimeKey:
		if stamp, ok := attr.Value.Any().(time.Time); ok {
			return slog.String(slog.TimeKey, stamp.Format("2006-01-02T15:04:05.000Z07:00"))
		}
	}
	return attr
}

// Path is where this logger writes, whether or not it is enabled.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Dropped counts log lines thrown away because the writer was behind. A
// stalled or full disk costs lines, never a frame: the render loop must
// never wait on the log.
func (l *Logger) Dropped() uint64 {
	if l == nil || l.sink == nil {
		return 0
	}
	return l.sink.dropped.Load()
}

func (l *Logger) Enabled(level slog.Level) bool {
	return l != nil && l.log != nil && level >= l.level
}

func (l *Logger) Level() slog.Level {
	if l == nil {
		return LevelOff
	}
	return l.level
}

func (l *Logger) Close() error {
	if l == nil || l.sink == nil {
		return nil
	}
	return l.sink.Close()
}

func (l *Logger) Log(level slog.Level, msg string, args ...any) {
	if !l.Enabled(level) {
		return
	}
	l.log.Log(context.Background(), level, msg, args...)
}

func (l *Logger) Trace(msg string, args ...any) { l.Log(LevelTrace, msg, args...) }
func (l *Logger) Debug(msg string, args ...any) { l.Log(LevelDebug, msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.Log(LevelInfo, msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.Log(LevelWarn, msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.Log(LevelError, msg, args...) }

// Enabled asks the process logger whether a level would be written, so a
// caller can skip building attributes it would only throw away.
func Enabled(level slog.Level) bool { return Default().Enabled(level) }

func Trace(msg string, args ...any) { Default().Log(LevelTrace, msg, args...) }
func Debug(msg string, args ...any) { Default().Log(LevelDebug, msg, args...) }
func Info(msg string, args ...any)  { Default().Log(LevelInfo, msg, args...) }
func Warn(msg string, args ...any)  { Default().Log(LevelWarn, msg, args...) }
func Error(msg string, args ...any) { Default().Log(LevelError, msg, args...) }

// PaneText writes captured terminal text, and only at trace.
//
// A pane running a coding agent routinely holds a live API key or OAuth
// token on screen, so this is the one thing the log never carries by
// default. Even asked for it is scrubbed, both here and again in the
// handler, because a log is a file somebody pastes into a bug report.
func PaneText(msg, sessionID, text string) {
	logger := Default()
	if !logger.Enabled(LevelTrace) {
		return
	}
	logger.Log(LevelTrace, msg, "session", sessionID, "pane", ScrubWrapped(tailOf(text, maxPaneBytes)))
}

// maxPaneBytes bounds one pane's contribution. The writer's queue counts
// records rather than bytes, and a trace-level board of ninety panes would
// otherwise be able to park hundreds of megabytes in it; scrubbing is also
// linear in this number, and it runs on the poll goroutine.
const maxPaneBytes = 8 << 10

// tailOf keeps the end of a capture, since a pane's newest output is at the
// bottom and that is the half worth having.
func tailOf(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := text[len(text)-max:]
	if line := strings.IndexByte(cut, '\n'); line >= 0 {
		cut = cut[line+1:]
	}
	return "...\n" + cut
}

// Err renders an error for a log line, keeping a nil error out of the record
// entirely so a success does not read as a failure with no message.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.String("err", err.Error())
}

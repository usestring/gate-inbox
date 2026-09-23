package logging

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// queueDepth is how far the writer may fall behind before lines start being
// dropped. Deep enough to absorb a rotation, which gzips the file it just
// closed, and shallow enough that the backlog stays bounded memory.
const queueDepth = 4096

// closeGrace bounds how long shutdown waits for the writer to drain. A
// wedged disk must cost a moment on exit, never a hung process.
const closeGrace = 2 * time.Second

// sink is the file behind the handler. Handler calls hand bytes to a
// channel and return; a single goroutine does the writing, so neither the
// render loop nor the poll loop ever waits on the disk.
type sink struct {
	queue chan []byte
	// stop, rather than closing queue, is what ends the writer. The poll
	// loop outlives the interface -- it has no stop of its own -- so it is
	// still logging while shutdown runs, and a send on a closed channel
	// panics. Shutting down has to cost that goroutine a dropped line at
	// worst.
	stop      chan struct{}
	done      chan struct{}
	rotator   *lumberjack.Logger
	path      string
	maxTotal  int64
	sweepBy   int64
	dropped   atomic.Uint64
	closeOnce sync.Once
}

func newSink(opts Options) (*sink, error) {
	// The log carries session names, working directories and command
	// lines, so the directory is the user's own rather than world-readable.
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o700); err != nil {
		return nil, err
	}
	sweepBy := int64(opts.MaxSizeMB) * 1 << 20 / 4
	if sweepBy < 64<<10 {
		sweepBy = 64 << 10
	}
	s := &sink{
		queue: make(chan []byte, queueDepth),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		rotator: &lumberjack.Logger{
			Filename:   opts.Path,
			MaxSize:    opts.MaxSizeMB,
			MaxBackups: opts.MaxBackups,
			Compress:   opts.Compress,
		},
		path:     opts.Path,
		maxTotal: int64(opts.MaxTotalMB) * 1 << 20,
		sweepBy:  sweepBy,
	}
	// A directory left oversized by an earlier run with different limits is
	// trimmed before the first line, not after the next rotation.
	pruneToTotal(s.path, s.maxTotal)
	go s.drain()
	return s, nil
}

// Write hands the record to the writer goroutine and returns. A full queue
// drops the line rather than blocking: a slow disk must degrade to a gap in
// the log, never to a frozen frame.
func (s *sink) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case s.queue <- buf:
	default:
		s.dropped.Add(1)
	}
	return len(p), nil
}

func (s *sink) drain() {
	defer close(s.done)
	var since int64
	for {
		select {
		case buf := <-s.queue:
			s.writeOne(buf, &since)
		case <-s.stop:
			// Whatever is already queued still belongs in the file: the
			// last records before a crash are the ones worth having.
			for {
				select {
				case buf := <-s.queue:
					s.writeOne(buf, &since)
				default:
					pruneToTotal(s.path, s.maxTotal)
					s.rotator.Close()
					return
				}
			}
		}
	}
}

func (s *sink) writeOne(buf []byte, since *int64) {
	// A write failure has nowhere to go: stderr belongs to the TUI.
	n, err := s.rotator.Write(buf)
	if err != nil {
		return
	}
	*since += int64(n)
	if *since >= s.sweepBy {
		*since = 0
		pruneToTotal(s.path, s.maxTotal)
	}
}

// Close stops the writer once the records already queued are on disk. A
// caller that keeps logging afterwards is not a bug to panic on -- it is the
// poll loop, which outlives the interface -- so its records are dropped.
func (s *sink) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		select {
		case <-s.done:
		case <-time.After(closeGrace):
		}
	})
	return nil
}

// pruneToTotal deletes the oldest rotated files until everything belonging
// to this log fits the cap.
//
// lumberjack's own MaxBackups already bounds the count, and Resolve derives
// that count from the cap, so this is the backstop for the cases arithmetic
// cannot cover: a cap lowered between runs, a file left by an older
// configuration, an uncompressed backup still waiting for its gzip. The
// active file is counted and never removed -- rotation, not deletion, is
// what bounds it.
func pruneToTotal(active string, maxTotal int64) []string {
	if maxTotal <= 0 {
		return nil
	}
	dir := filepath.Dir(active)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	base := filepath.Base(active)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	type file struct {
		path string
		size int64
		when time.Time
	}
	// Age comes from the timestamp lumberjack puts in the name, never from
	// the mtime: compressing rewrites that, and it compresses newest-first,
	// so a batch of two backups milled at once leaves the newer file looking
	// like the older one and the sweep would delete the recent history.
	age := func(name string, info os.FileInfo) time.Time {
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, stem+"-"), ".gz")
		stamp = strings.TrimSuffix(stamp, ext)
		if when, err := time.Parse("2006-01-02T15-04-05.000", stamp); err == nil {
			return when
		}
		return info.ModTime()
	}
	var total int64
	var rotated []file
	for _, entry := range entries {
		if entry.IsDir() || !belongsTo(entry.Name(), base, stem, ext) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		if entry.Name() == base {
			continue
		}
		rotated = append(rotated, file{filepath.Join(dir, entry.Name()), info.Size(), age(entry.Name(), info)})
	}
	if total <= maxTotal {
		return nil
	}
	sort.Slice(rotated, func(i, j int) bool { return rotated[i].when.Before(rotated[j].when) })
	var removed []string
	for _, candidate := range rotated {
		if total <= maxTotal {
			break
		}
		err := os.Remove(candidate.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// A file lumberjack's own mill deleted first is gone either way, so
		// it stops counting toward the total; leaving it in would take a
		// live backup with it.
		total -= candidate.size
		if err == nil {
			removed = append(removed, candidate.path)
		}
	}
	return removed
}

// Owns reports whether a file in the log directory is one this log wrote.
// A configured path may put the log beside state.db and config.toml, and
// neither a sweep nor a listing may treat those as its own.
func Owns(active, name string) bool {
	base := filepath.Base(active)
	ext := filepath.Ext(base)
	return belongsTo(name, base, strings.TrimSuffix(base, ext), ext)
}

// belongsTo matches the active file and the rotated siblings lumberjack
// names after it ("<stem>-<timestamp><ext>", plus ".gz" once compressed),
// so a sweep can never reach a file this log did not write.
func belongsTo(name, base, stem, ext string) bool {
	if name == base {
		return true
	}
	if !strings.HasPrefix(name, stem+"-") {
		return false
	}
	return strings.HasSuffix(name, ext) || strings.HasSuffix(name, ext+".gz")
}

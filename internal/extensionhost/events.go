package extensionhost

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// maxQueued is how many status events one subscriber may fall behind by
// before the oldest are dropped. A subscriber that far behind is stuck, and
// the board's memory is not its to hold.
const maxQueued = 4096

// Events is the board's side of extension.BoardHost: the poll pass reports
// to it, and it fans what it is told out to the extensions' subscriptions,
// each on a goroutine of its own so the pass never waits on an extension.
type Events struct {
	board extension.Board
	// report is told about a subscriber that panicked or fell behind, by
	// the ID of the extension that owns it.
	report func(owner string, err error)

	mu     sync.Mutex
	events []*subscription[extension.StatusEvent]
	passes []*subscription[extension.Pass]
}

// NewEvents fans the poll pass out beside board, which every BoardHost it
// lends reads and answers through. report may be nil.
func NewEvents(board extension.Board, report func(owner string, err error)) *Events {
	if report == nil {
		report = func(string, error) {}
	}
	return &Events{board: board, report: report}
}

// For is the BoardHost lent to the extension with id, and the release that
// ends every subscription made through it.
func (b *Events) For(id string) (extension.BoardHost, func()) {
	view := &boardView{Board: b.board, events: b, owner: id}
	return view, view.release
}

// Transition is a poll pass having stored a status change.
func (b *Events) Transition(id, from, to string, at time.Time) {
	event := extension.StatusEvent{SessionID: id, From: from, To: to, Kind: extension.KindOf(to), At: at}
	b.mu.Lock()
	subs := b.events
	b.mu.Unlock()
	for _, sub := range subs {
		sub.push(event)
	}
}

// Pass is a poll pass having finished with sessions.
func (b *Events) Pass(at time.Time, sessions []store.Session) {
	b.mu.Lock()
	subs := b.passes
	b.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	pass := extension.Pass{At: at, Sessions: make([]extension.SessionStatus, 0, len(sessions))}
	for _, sess := range sessions {
		if sess.Archived {
			continue
		}
		pass.Sessions = append(pass.Sessions, extension.SessionStatus{ID: sess.ID, Status: sess.Status})
	}
	for _, sub := range subs {
		// Each subscriber gets its own slice, so one that keeps or edits
		// what it was handed cannot change what the next one reads.
		copied := pass
		copied.Sessions = append([]extension.SessionStatus(nil), pass.Sessions...)
		sub.push(copied)
	}
}

func (b *Events) remove(sub any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = without(b.events, sub)
	b.passes = without(b.passes, sub)
}

// without copies rather than filtering in place: Transition and Pass range
// over the slice they read outside the lock.
func without[T any](subs []*subscription[T], drop any) []*subscription[T] {
	out := make([]*subscription[T], 0, len(subs))
	for _, sub := range subs {
		if any(sub) != drop {
			out = append(out, sub)
		}
	}
	return out
}

type boardView struct {
	extension.Board
	events *Events
	owner  string

	mu       sync.Mutex
	subs     []func()
	released bool
}

func (v *boardView) Logger() *slog.Logger {
	return logging.Slog().With("extension", v.owner)
}

func (v *boardView) Tracer() extension.Tracer { return tracer{scope: v.owner} }

func (v *boardView) Subscribe(fn func(extension.StatusEvent)) func() {
	sub := newSubscription(v.owner, fn, v.events.report, false)
	return v.add(sub, func() {
		v.events.mu.Lock()
		v.events.events = append(v.events.events[:len(v.events.events):len(v.events.events)], sub)
		v.events.mu.Unlock()
	})
}

func (v *boardView) OnPass(fn func(extension.Pass)) func() {
	sub := newSubscription(v.owner, fn, v.events.report, true)
	return v.add(sub, func() {
		v.events.mu.Lock()
		v.events.passes = append(v.events.passes[:len(v.events.passes):len(v.events.passes)], sub)
		v.events.mu.Unlock()
	})
}

type closer interface{ close() }

// add registers sub unless the view has been released, which is what keeps
// a goroutine an extension left behind from subscribing again after its
// extension failed or the board stopped it.
func (v *boardView) add(sub closer, register func()) func() {
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			v.events.remove(sub)
			sub.close()
		})
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.released {
		sub.close()
		return func() {}
	}
	register()
	v.subs = append(v.subs, unsubscribe)
	return unsubscribe
}

func (v *boardView) release() {
	v.mu.Lock()
	subs := v.subs
	v.subs, v.released = nil, true
	v.mu.Unlock()
	for _, unsubscribe := range subs {
		unsubscribe()
	}
}

// subscription is one callback and the goroutine that calls it. latest
// keeps only the newest value queued, which is what a pass wants; otherwise
// every value is delivered, in order, up to maxQueued behind.
type subscription[T any] struct {
	owner  string
	fn     func(T)
	report func(string, error)
	latest bool

	mu      sync.Mutex
	queue   []T
	dropped int
	closed  bool
	wake    chan struct{}
}

func newSubscription[T any](owner string, fn func(T), report func(string, error), latest bool) *subscription[T] {
	sub := &subscription[T]{owner: owner, fn: fn, report: report, latest: latest, wake: make(chan struct{}, 1)}
	go sub.run()
	return sub
}

func (s *subscription[T]) push(value T) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	switch {
	case s.latest:
		s.queue = append(s.queue[:0], value)
	case len(s.queue) >= maxQueued:
		s.queue = append(s.queue[1:], value)
		s.dropped++
	default:
		s.queue = append(s.queue, value)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// close stops delivery: nothing queued is delivered after it, though a
// callback already running finishes.
func (s *subscription[T]) close() {
	s.mu.Lock()
	s.closed, s.queue = true, nil
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscription[T]) run() {
	for range s.wake {
		for {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			value := s.queue[0]
			s.queue = s.queue[1:]
			dropped := s.dropped
			s.dropped = 0
			s.mu.Unlock()
			if dropped > 0 {
				s.report(s.owner, fmt.Errorf("a status subscriber fell behind; %d event(s) dropped", dropped))
			}
			s.call(value)
		}
	}
}

func (s *subscription[T]) call(value T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.report(s.owner, fmt.Errorf("a board subscriber panicked: %v", recovered))
		}
	}()
	s.fn(value)
}

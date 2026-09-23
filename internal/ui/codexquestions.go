package ui

import (
	"sync"

	"github.com/usestring/gate-inbox/internal/codexq"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Codex loses the questions it asks and the pane cannot be used to notice: a
// blocking dialog auto-resolves itself empty and leaves, and an async question
// never draws a dialog at all. internal/codexq documents both and reads the
// rollout for what actually happened.

// unansweredCodexQuestions returns the questions this session was asked and
// never answered, and has not since been back to the session about.
//
// The tracker per session is what keeps this off the critical path: the board
// polls every row about once a second and a rollout reaches megabytes, so each
// pass reads only what was appended since the last one. Its first read is the
// exception -- that one is the whole conversation, and it is handed to
// codexSeeder rather than paid here.
//
// A session with no conversation id yet, a tool that is not codex, a rollout
// that cannot be read, or one whose first read has not landed yet returns
// none: the pane keeps deciding, exactly as before.
func (p *poller) unansweredCodexQuestions(sess store.Session) []codexq.Question {
	if sess.Tool != search.ToolCodex || p.locator == nil || sess.AgentSessionID == "" {
		return nil
	}
	// Target caches into the locator's own maps, so it stays on the pass: a
	// seed goroutine calling it would race every other reader of the locator.
	target, ok := p.locator.Target(sess.ID, sess.Tool, sess.Cwd, sess.AgentSessionID)
	if !ok || target.Path == "" {
		return nil
	}
	if p.codexQuestions == nil {
		p.codexQuestions = map[string]*codexq.Tracker{}
	}
	tracker, seen := p.codexQuestions[sess.ID]
	if !seen {
		tracker = p.codexSeeds.adopt(sess.ID, target.Path)
		if tracker == nil {
			return nil
		}
		p.codexQuestions[sess.ID] = tracker
	}
	questions, err := tracker.Update(target.Path)
	if err != nil {
		return nil
	}
	return codexq.Unresolved(questions)
}

// codexSeed is one finished rollout read, carrying the path it read so a
// session that has since moved to another conversation cannot adopt it.
type codexSeed struct {
	path    string
	tracker *codexq.Tracker
}

// codexSeeder does each tracker's first rollout read on its own goroutine.
//
// Every later Update costs microseconds because it reads only what was
// appended, but the first one parses the whole conversation, and these files
// are not small: a 5 MB rollout costs a fifth of a second, and a long-lived
// session's runs to hundreds of megabytes and several seconds. Paid inline,
// that read landed in the status phase of a poll pass and stalled every row on
// the board rather than just the codex ones -- once per manager start, per
// revive, and every time forgetVanished drops a tracker. So the pass asks for
// a seed, carries on, and adopts the tracker on a later pass.
//
// The handover is what keeps codexq.Tracker free of locks: a seed builds its
// tracker privately and only publishes it once Update has returned, after
// which the pass is again the single goroutine that touches it.
type codexSeeder struct {
	mu sync.Mutex
	// inflight is the path each running seed is reading, and doubles as its
	// cancellation token: clearing or replacing the entry makes the goroutine
	// drop what it read rather than publish a tracker for a rollout its
	// session has left behind.
	inflight map[string]string
	ready    map[string]codexSeed
	// slots bounds how many rollouts are read at once. A board that comes up
	// with a dozen codex rows would otherwise pull every one of their
	// conversations through the page cache simultaneously, which is the cost
	// this is trying to keep off the box, not just off the pass.
	slots chan struct{}
}

const codexSeedsAtOnce = 2

// adopt hands back the tracker seeded for path, starting the seed if none is
// running for it. It returns nil while the read is still going, and the caller
// reports no questions for that session -- see unansweredCodexQuestions for
// why a pass or two of that is safe.
func (s *codexSeeder) adopt(id, path string) *codexq.Tracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed, held := s.ready[id]; held {
		delete(s.ready, id)
		if seed.path == path {
			return seed.tracker
		}
	}
	if s.inflight[id] == path {
		return nil
	}
	if s.inflight == nil {
		s.inflight = map[string]string{}
	}
	if s.slots == nil {
		s.slots = make(chan struct{}, codexSeedsAtOnce)
	}
	s.inflight[id] = path
	go s.seed(id, path)
	return nil
}

func (s *codexSeeder) seed(id, path string) {
	s.slots <- struct{}{}
	defer func() { <-s.slots }()
	if !s.stillWanted(id, path) {
		// Cancelled while this seed waited its turn; the read is the
		// expensive part and nothing is left to give it to.
		return
	}
	tracker := &codexq.Tracker{}
	// A failed read is published too. The tracker has taken nothing from the
	// file, so the next pass's Update retries it and returns no questions
	// meanwhile -- the same answer a failed inline read gave.
	_, _ = tracker.Update(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[id] != path {
		return
	}
	delete(s.inflight, id)
	if s.ready == nil {
		s.ready = map[string]codexSeed{}
	}
	s.ready[id] = codexSeed{path: path, tracker: tracker}
}

func (s *codexSeeder) stillWanted(id, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[id] == path
}

// forget drops everything held for sessions the store no longer lists, and
// cancels their reads: a seed that outlives its session would otherwise
// publish a tracker nobody will ever adopt.
func (s *codexSeeder) forget(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.ready {
		if !live[id] {
			delete(s.ready, id)
		}
	}
	for id := range s.inflight {
		if !live[id] {
			delete(s.inflight, id)
		}
	}
}

// codexQuestionStatus upgrades a derived status when the session is sitting on
// a question nobody answered.
//
// Working is left alone on purpose. An async question does not stop the agent,
// and a row that is genuinely mid-turn should say so; overriding it would make
// every session that ever asked something read waiting for the rest of its
// life. What matters is the moment the turn settles: that is when the question
// would otherwise disappear without trace, and it is where waiting belongs.
//
// Errored is left alone too. An error is the more urgent thing to show, and a
// crashed session's unanswered question is not what the operator needs first.
func codexQuestionStatus(derived string, unanswered []codexq.Question) string {
	if len(unanswered) == 0 {
		return derived
	}
	switch derived {
	case status.Finished, status.Idle:
		return status.Waiting
	}
	return derived
}

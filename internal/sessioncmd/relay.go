package sessioncmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Whose answer an answer is.
//
// Claude Code gives its auto-mode classifier an AskUserQuestion answer as the
// user's own input, so an answer a parent keys into its child reads there as
// the child's user approving whatever the question asked. Nothing here asks
// the child to trust Gate Inbox; it only narrows what a parent can key and
// records what it did key:
//
//   - every answer a parent keys is written to the dialog_answers ledger
//     before the first keystroke, as the parent's own (agent) or as the
//     user's, relayed;
//   - relayed means the parent's own transcript holds an AskUserQuestion with
//     the child's question and options word for word, answered after the child
//     asked, with this same answer; that answer is spent once, and one Gate
//     Inbox typed into the parent is not its user's;
//   - a child question headed Approval is the child asking for its user's
//     approval, so only a relayed answer is keyed into it.
//
// The child's PostToolUse hook reads the ledger back (see HookAskAnswered).

var (
	errApprovalNeedsRelay = errors.New("this is the child asking for your user's approval; put it to your " +
		"user verbatim with your own question tool (same header, question and options) and answer with " +
		"relay: true")
	errRelayRefused = errors.New("relay refused")
)

// answerGuard is what a parent's answer is checked against and recorded in,
// before any key reaches the child.
type answerGuard struct {
	sessions *Sessions
	store    *store.Store
	caller   store.Session
	target   store.Session
	relay    bool
	call     convo.AskCall
	haveCall bool
	rows     []int64
}

func (s *Sessions) guard(st *store.Store, caller, target store.Session, relay bool) *answerGuard {
	g := &answerGuard{sessions: s, store: st, caller: caller, target: target, relay: relay}
	if path := s.transcriptOf(target); path != "" {
		g.call, g.haveCall = convo.PendingAskCall(path)
	}
	return g
}

func (s *Sessions) transcriptOf(sess store.Session) string {
	if sess.AgentSessionID == "" {
		return ""
	}
	return convo.TranscriptFor(s.claudeHome, sess.AgentSessionID, sess.Cwd)
}

// plannedAnswer is one answer about to be keyed: the 0-based index of the
// question it answers, or -1 when that cannot be told, and the answer.
type plannedAnswer struct {
	index  int
	answer string
}

// admit checks every planned answer and writes its ledger row, pending. It
// runs before the first keystroke; an error keys nothing. screen is the
// dialog's questions as read off the pane, used for headers when the
// child's transcript cannot be read.
func (g *answerGuard) admit(planned []plannedAnswer, screen []dialog.Question) error {
	type row struct {
		question convo.AskQuestion
		answer   string
		evidence string
	}
	rows := make([]row, 0, len(planned))
	for _, p := range planned {
		question, known := g.question(p.index, screen)
		if dialog.IsApproval(question.Header) && !g.relay {
			return errApprovalNeedsRelay
		}
		r := row{question: question, answer: p.answer}
		if g.relay {
			if !known || !g.haveCall {
				return fmt.Errorf("%w: session %s's own transcript does not show the question it is holding, "+
					"so there is nothing to match your user's answer against; a person answers it at its pane",
					errRelayRefused, g.target.ID)
			}
			evidence, err := g.verify(question, p.answer)
			if err != nil {
				return err
			}
			r.evidence = evidence
		}
		rows = append(rows, r)
	}
	mode := store.AnswerByAgent
	if g.relay {
		mode = store.AnswerRelayedUser
	}
	for _, r := range rows {
		id, err := g.store.RecordAnswer(store.DialogAnswer{
			TargetSession:     g.target.ID,
			TargetToolUseID:   g.call.ToolUseID,
			QuestionHash:      questionHash(r.question),
			Answer:            r.answer,
			BySession:         g.caller.ID,
			Mode:              mode,
			EvidenceToolUseID: r.evidence,
		})
		if errors.Is(err, store.ErrEvidenceUsed) {
			g.finish(errRelayRefused)
			return fmt.Errorf("%w: your user's answer to that question has already been relayed once; "+
				"ask them again", errRelayRefused)
		}
		if err != nil {
			g.finish(err)
			return fmt.Errorf("cannot record the answer before keying it, so nothing was keyed: %w", err)
		}
		g.rows = append(g.rows, id)
	}
	return nil
}

// finish marks the rows admit wrote as keyed, or failed when err is set.
func (g *answerGuard) finish(err error) {
	if g == nil {
		return
	}
	state := store.AnswerKeyed
	if err != nil {
		state = store.AnswerFailed
	}
	for _, id := range g.rows {
		if markErr := g.store.MarkAnswer(id, state); markErr != nil {
			logging.Warn("could not mark a dialog answer", "id", id, "err", markErr)
		}
	}
	g.rows = nil
}

// question is the child's question at index, from its transcript when that
// can be read and from the screen otherwise. known says it came from the
// transcript.
func (g *answerGuard) question(index int, screen []dialog.Question) (convo.AskQuestion, bool) {
	if index >= 0 && g.haveCall && index < len(g.call.Questions) {
		return g.call.Questions[index], true
	}
	if index >= 0 && index < len(screen) {
		return convo.AskQuestion{Header: screen[index].Header, Question: screen[index].Question}, false
	}
	if index < 0 && g.haveCall && len(g.call.Questions) == 1 {
		return g.call.Questions[0], true
	}
	return convo.AskQuestion{}, false
}

// verify finds the parent's own dialog that settles the child's question
// with answer, and returns its tool_use id.
func (g *answerGuard) verify(child convo.AskQuestion, answer string) (string, error) {
	refuse := func(format string, args ...any) (string, error) {
		return "", fmt.Errorf("%w: "+format, append([]any{errRelayRefused}, args...)...)
	}
	if g.call.AskedAt.IsZero() {
		return refuse("the child's question carries no time, so your user's answer cannot be shown to come after it")
	}
	path := g.sessions.transcriptOf(g.caller)
	if path == "" {
		return refuse("this session's own transcript cannot be found, so there is no dialog of your user's to match")
	}
	asks, err := convo.AnsweredAsks(path, time.Time{})
	if err != nil {
		return refuse("cannot read this session's own transcript: %v", err)
	}
	hash := questionHash(child)
	var reason string
	note := func(r string) {
		if reason == "" {
			reason = r
		}
	}
	for i := len(asks) - 1; i >= 0; i-- {
		ask := asks[i]
		for _, q := range ask.Questions {
			if !sameAsk(q, child) {
				continue
			}
			given, ok := answerFor(ask.Answers, q.Question)
			switch {
			case !ok:
				note("your dialog asking it holds no answer to it")
			case !ask.AnsweredAt.After(g.call.AskedAt):
				note("your user answered that question before the child asked it; ask them again")
			case normalise(given) != normalise(answer):
				note(fmt.Sprintf("your user answered %q, not %q", given, answer))
			default:
				used, err := g.store.EvidenceUsed(ask.ToolUseID, hash)
				if err != nil {
					return refuse("cannot read the answer ledger: %v", err)
				}
				if used {
					note("your user's answer to that question has already been relayed once; ask them again")
					continue
				}
				typed, err := g.store.AgentAnswered(g.caller.ID, ask.ToolUseID)
				if err != nil {
					return refuse("cannot read the answer ledger: %v", err)
				}
				if typed {
					note("that answer in your own dialog was typed by the session that spawned you, " +
						"not by your user")
					continue
				}
				return ask.ToolUseID, nil
			}
		}
	}
	if reason == "" {
		reason = "no dialog of yours asks the child's question with its options word for word; put it to " +
			"your user verbatim (same question and options) and relay what they choose"
	}
	return refuse("%s", reason)
}

func answerFor(answers map[string]string, question string) (string, bool) {
	if given, ok := answers[question]; ok {
		return given, true
	}
	for key, given := range answers {
		if normalise(key) == normalise(question) {
			return given, true
		}
	}
	return "", false
}

// sameAsk is the parent's question and the child's matching word for word,
// ignoring only how whitespace runs: text, options in order, multi-select.
func sameAsk(a, b convo.AskQuestion) bool {
	if normalise(a.Question) != normalise(b.Question) || a.MultiSelect != b.MultiSelect ||
		len(a.Options) != len(b.Options) {
		return false
	}
	for i := range a.Options {
		if normalise(a.Options[i].Label) != normalise(b.Options[i].Label) {
			return false
		}
	}
	return true
}

func normalise(text string) string { return strings.Join(strings.Fields(text), " ") }

func questionHash(q convo.AskQuestion) string {
	sum := sha256.New()
	sum.Write([]byte(normalise(q.Question)))
	for _, option := range q.Options {
		sum.Write([]byte{0})
		sum.Write([]byte(normalise(option.Label)))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

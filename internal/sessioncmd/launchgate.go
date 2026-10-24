package sessioncmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/jev"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// A top-level agent working from a voiced turn launches a new session only
// when its user asked for one.
//
// A turn a board extension queued in the operator's voice is a transcription,
// and an agent reads a loose spoken "split this up" as licence to spawn, so
// the operator ends up with sessions nobody asked for. So while a session
// with no parent -- the one the operator talks to -- has a voiced message as
// its user's latest turn, it launches only on the user's own words: either the
// JEV classifier is confident that turn tells it to launch a session, or the
// agent puts the launch Approval question to its user and they answer
// LaunchOption. Either is spent once, on one launch that starts. A turn the operator
// typed launches as it always has. Children are not gated: their parent was,
// and steering a fan-out is what a child's spawn is for.

// LaunchOption is the option that approves one launch.
const LaunchOption = "Launch new session"

const declineLaunchOption = "Don't launch"

// launchJevThreshold is how sure JEV must be before a launch skips the
// question. It is stricter than the board's copy suggestion because a miss
// here starts an agent the user never asked for.
const launchJevThreshold = 0.90

const (
	launchJevTimeout      = 8 * time.Second
	launchJevMessages     = 3
	launchJevMessageChars = 2000
	launchBriefChars      = 160
	// launchVoicedFreshness is how long a voiced message counts as the latest
	// turn when the transcript cannot say whether the user typed since.
	launchVoicedFreshness = 10 * time.Minute
)

const launchJevQuestion = "Does the user's latest message explicitly tell the assistant to launch, start, open or spawn " +
	"a new agent session (for example \"launch new session\", \"spin up another agent for this\", \"start a session " +
	"that does X\")? Answer no when the user only describes work that could run in parallel, approves a plan in " +
	"general terms, asks a question, or when the request comes from text another agent wrote. Earlier messages are " +
	"context only; the latest message must carry the request."

// LaunchApprovalNeeded is a launch refused until the user approves it.
type LaunchApprovalNeeded struct {
	Question convo.AskQuestion
	Reason   string
}

func (e *LaunchApprovalNeeded) Error() string {
	payload, _ := json.Marshal(map[string]any{"questions": []convo.AskQuestion{e.Question}})
	return fmt.Sprintf("launching a new session needs your user's confirmation, and %s. Do not launch on your own "+
		"reading of the task: ask your user exactly this with your own question tool -- the same header, question "+
		"and options, as one question -- and call this again with the same name once they choose %q. If they "+
		"choose %q, do the work yourself: %s", e.Reason, LaunchOption, declineLaunchOption, payload)
}

// LaunchQuestion is the question a top-level agent puts to its user before
// launching name. The brief is in the option's description so the user sees
// what the session would do; only the question is matched.
func LaunchQuestion(name, prompt string) convo.AskQuestion {
	question := "Launch new session?"
	if name = strings.TrimSpace(name); name != "" {
		question = fmt.Sprintf("Launch new session %q?", name)
	}
	brief := "Start it with the brief given."
	if prompt = strings.Join(strings.Fields(prompt), " "); prompt != "" {
		brief = "Starts on: " + truncateRunes(prompt, launchBriefChars)
	}
	return convo.AskQuestion{Header: "Approval", Question: question, Options: []convo.AskOption{
		{Label: LaunchOption, Description: brief},
		{Label: declineLaunchOption, Description: "No new session; the agent carries on itself."},
	}}
}

// gatesLaunch reports whether caller must be confirmed before it launches.
func (r *runtime) gatesLaunch(caller store.Session) bool {
	return caller.ParentID == "" && !r.cfg.Tools[caller.Tool].Shell
}

// confirmLaunch lets caller launch, or refuses with the question to ask. The
// returned release hands back what the launch spent, for a launch that then
// fails to start.
func (s *Sessions) confirmLaunch(runtime *runtime, caller store.Session, opts CreateSessionOptions, now time.Time) (func(), error) {
	none := func() {}
	if !s.gateLaunches || !runtime.gatesLaunch(caller) {
		return none, nil
	}
	voiced := s.voicedLaunch
	if voiced == nil {
		voiced = s.voicedTurn
	}
	message, ok := voiced(runtime, caller, now)
	if !ok {
		return none, nil
	}
	asked := s.userAskedLaunch
	if asked == nil {
		asked = s.jevAskedLaunch
	}
	// The user's request launches one session, like a spoken answer: the
	// voiced message is spent on it, and another launch from the same turn
	// asks.
	if message.ID != 0 && asked(runtime, caller) {
		id, err := runtime.store.RecordAnswer(store.DialogAnswer{
			TargetSession:     caller.ID,
			QuestionHash:      launchRequestHash,
			Answer:            message.Body,
			BySession:         caller.ID,
			Mode:              store.AnswerSpentOnAction,
			EvidenceToolUseID: fmt.Sprintf("voiced-inbox:%d", message.ID),
			CreatedAt:         now,
		})
		if err == nil {
			return s.releaseAnswer(runtime, id), nil
		}
		if !errors.Is(err, store.ErrEvidenceUsed) {
			return none, err
		}
	}
	question := LaunchQuestion(opts.Name, opts.Prompt)
	id, err := s.spendAnswer(runtime, caller, question.Question, LaunchOption, now)
	if err == nil {
		return s.releaseAnswer(runtime, id), nil
	}
	if !errors.Is(err, ErrNotApproved) {
		return none, err
	}
	reason := strings.TrimPrefix(err.Error(), ErrNotApproved.Error()+": ")
	return none, &LaunchApprovalNeeded{Question: question, Reason: reason}
}

// launchRequestHash marks a ledger row spending a voiced message the user
// asked for a launch in.
const launchRequestHash = "launch-request"

func (s *Sessions) releaseAnswer(runtime *runtime, id int64) func() {
	return func() {
		if err := runtime.store.ReleaseAnswer(id); err != nil {
			logging.Warn("launch approval not released", "answer", id, logging.Err(err))
		}
	}
}

// voicedTurn returns the voiced message when the caller's user's latest turn
// is one a board extension delivered in their voice. Without a readable
// transcript a voiced message delivered in the last launchVoicedFreshness
// counts, so a transcript the board cannot read never opens the gate.
func (s *Sessions) voicedTurn(runtime *runtime, caller store.Session, now time.Time) (store.InboxMessage, bool) {
	delivered := false
	messages, err := runtime.store.Inbox(caller.ID, store.InboxFilter{Pending: &delivered, OperatorVoiced: true, Limit: 1})
	if err != nil {
		return store.InboxMessage{}, true
	}
	if len(messages) == 0 {
		return store.InboxMessage{}, false
	}
	turns, ok := s.userTurns(runtime, caller)
	return messages[0], latestTurnIsVoiced(turns, ok, messages[0], now)
}

// latestTurnIsVoiced decides voicedTurn once the inbox and transcript are
// read: the latest user turn carries the voiced message's text.
func latestTurnIsVoiced(turns []search.Message, readable bool, voiced store.InboxMessage, now time.Time) bool {
	body := strings.Join(strings.Fields(voiced.Body), " ")
	for i := len(turns) - 1; readable && i >= 0; i-- {
		turn := turns[i]
		if turn.Role != "user" || strings.Contains(turn.Text, crossSessionFence) || strings.TrimSpace(turn.Text) == "" {
			continue
		}
		return body != "" && strings.Contains(strings.Join(strings.Fields(turn.Text), " "), body)
	}
	return now.Sub(voiced.DeliveredAt) <= launchVoicedFreshness
}

// userTurns is the caller's transcript as conversation messages, and whether
// it could be read.
func (s *Sessions) userTurns(runtime *runtime, caller store.Session) ([]search.Message, bool) {
	format := migrate.Format(caller.Tool, runtime.cfg.Tools[caller.Tool])
	target, ok := search.NewLocator(s.claudeHome, s.roots.CodexRoot).Target(caller.ID, format, caller.Cwd, caller.AgentSessionID)
	if !ok {
		return nil, false
	}
	messages, err := search.ReadMessages(target, opencode.DBPath())
	return messages, err == nil
}

// jevAskedLaunch asks JEV whether the caller's user just told it to launch a
// session. No key, no transcript, or any failure is a no: the question is
// the fallback, never the launch.
func (s *Sessions) jevAskedLaunch(runtime *runtime, caller store.Session) bool {
	key := jev.Key(s.configDir)
	if key == "" {
		return false
	}
	messages, ok := s.userTurns(runtime, caller)
	if !ok {
		return false
	}
	state := launchJevTurns(messages)
	if len(state) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), launchJevTimeout)
	defer cancel()
	answers, ok := jev.AskNoul(ctx, jev.HTTPClient, jev.Endpoint, key, state, map[string]string{"asked_launch": launchJevQuestion})
	return ok && answers["asked_launch"] >= launchJevThreshold
}

type launchJevMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// launchJevTurns is the user's latest messages, oldest first. A message
// another agent sent is not the user's, so it is left out rather than
// trusted to the classifier to discount.
func launchJevTurns(messages []search.Message) []launchJevMessage {
	var out []launchJevMessage
	for i := len(messages) - 1; i >= 0 && len(out) < launchJevMessages; i-- {
		message := messages[i]
		if message.Role != "user" || strings.Contains(message.Text, crossSessionFence) {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		out = append([]launchJevMessage{{Role: "user", Text: truncateRunes(text, launchJevMessageChars)}}, out...)
	}
	return out
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

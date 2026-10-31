package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/jev"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// The JEV finish check reads a Claude session's transcript each time the row
// turns finished and asks JEV two yes-no questions about it in one call:
// whether the work is done, and whether the agent stopped because it had no
// Chrome browser tools. The second has a fixed remedy, so the board applies
// it without asking: end the pane, resume the same conversation with
// --chrome, and tell the agent to continue.
//
// Only a confident answer acts. A finished score short of confident but at
// least jevFinishProbeLow asks the agent itself, by typing jevFinishProbe
// into the idle session; its DONE or NOT DONE reply settles the finish on
// the next pass without asking JEV again. Anything else, a missing key, a
// failed request or an unreadable transcript leaves the row as it was.

const jevFinishCheckSetting = "experimental_jev_finish_check"

const (
	jevFinishBandLow  = 0.20
	jevFinishBandHigh = 0.80
	// jevFinishProbeLow is where asking the agent starts. TestJevFinishEval
	// put about three quarters of the sessions the user went on to close at
	// or above it, against half at jevFinishBandHigh; the agent's own answer
	// is what filters out the unfinished ones it also lets through.
	jevFinishProbeLow = 0.50
	// jevFinishMessages is how many of the latest turns the state carries,
	// each cut to jevFinishMessageChars. Both were picked with
	// TestJevFinishEval: more turns or longer turns did not flag more
	// finished sessions without also flagging more unfinished ones.
	jevFinishMessages     = 4
	jevFinishMessageChars = 1500
	jevFinishTimeout      = 6 * time.Second
	chromeFlag            = "--chrome"
	jevFinishProbePrefix  = "[Gate Inbox finish probe] "
)

const (
	// The question says outright that optional follow-ups and a PR awaiting
	// review still count as finished: asked only for "no work left open",
	// JEV read those as open and scored most sessions the user then closed
	// as unfinished.
	jevFinishedQuestion = "Is this session at its end state: the agent has finished the task it was given and reported the result, " +
		"so the user could close the session now? Count it as finished even if the agent offers optional follow-ups or a PR awaits " +
		"review; count it as not finished if the agent stopped mid-task, hit an error or blocker it did not resolve, or asked a " +
		"question it needs answered to continue."
	jevMissingChromeQuestion = "Did the agent stop, or fail to do what was asked, because it had no Chrome browser control: " +
		"the Claude in Chrome tools (mcp__claude-in-chrome__*) were missing or unavailable, the browser extension was not " +
		"connected, or it said the session must be started with --chrome?"
	jevFinishProbe = "Gate Inbox finish check, not a new task: is the work you were given in this session finished? " +
		"Reply with exactly DONE if it is, or NOT DONE and one line on what is left. Do nothing else."
)

func storedJevFinishCheck(st *store.Store) bool {
	value, err := st.Setting(jevFinishCheckSetting)
	return err == nil && value == "on"
}

// jevFinishState is what the check remembers between passes. checked holds
// the finish each row was last asked about, so one finish costs one call;
// chromeRelaunched holds the rows already relaunched with Chrome, so a
// session that still reads as missing it is not relaunched in a loop.
type jevFinishState struct {
	busy             bool
	checked          map[string]string
	attended         map[string]string
	chromeRelaunched map[string]bool
}

type jevFinishVerdict struct {
	finished      float64
	missingChrome float64
}

type jevFinishResultMsg struct {
	id       string
	identity string
	verdict  jevFinishVerdict
	ok       bool
	// probe is the agent's answer when the finish is its reply to
	// jevFinishProbe; JEV is not asked about such a finish.
	probe jevFinishProbeAnswer
}

type jevFinishProbeAnswer int

const (
	jevProbeNone jevFinishProbeAnswer = iota
	jevProbeDone
	jevProbeNotDone
)

// jevFinishProbeReply reads the latest exchange. It is an answer only when
// the last user turn is the probe: the transcript, not board state, records
// that the agent was asked, so the answer survives a board restart.
func jevFinishProbeReply(messages []search.Message) jevFinishProbeAnswer {
	reply := ""
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "assistant":
			if reply == "" {
				reply = messages[i].Text
			}
		case "user":
			if strings.TrimSpace(messages[i].Text) != jevFinishProbe {
				return jevProbeNone
			}
			// Agents bold or quote a one-word answer as often as not.
			word := strings.ToUpper(strings.TrimLeft(reply, "*_`#> \t\n"))
			switch {
			case strings.HasPrefix(word, "DONE"):
				return jevProbeDone
			case word != "":
				return jevProbeNotDone
			}
			return jevProbeNone
		}
	}
	return jevProbeNone
}

// jevFinishIdentity names one finish of one row: the same row finishing
// again after more work is a new finish to ask about.
func jevFinishIdentity(sess store.Session) string {
	return sess.ID + "\x00" + sess.LastStatusAt.UTC().Format(time.RFC3339Nano)
}

// jevFinishCandidate picks the next finished Claude row not yet asked about.
// A pane the manager did not start is left alone, since the remedy kills it.
func (m *Model) jevFinishCandidate() (store.Session, string, bool) {
	formats := historyToolFormats(m.cfg)
	for _, sess := range m.sessions {
		if sess.Archived || formats[sess.Tool] != search.ToolClaude || sess.Status != status.Finished || sess.TmuxPaneID != "" {
			continue
		}
		identity := jevFinishIdentity(sess)
		if m.jevFinish.checked[sess.ID] == identity || m.jevFinishAttended(sess) {
			continue
		}
		return sess, identity, true
	}
	return store.Session{}, "", false
}

func (m *Model) noteJevFinishActivity() {
	if !m.jevFinishCheck {
		return
	}
	sess, ok := m.sessionByID(m.focusedID)
	if !ok || sess.Status != status.Finished {
		return
	}
	if m.jevFinish.attended == nil {
		m.jevFinish.attended = map[string]string{}
	}
	m.jevFinish.attended[sess.ID] = jevFinishIdentity(sess)
	m.poller.mu.Lock()
	if m.poller.jevFinishAttended == nil {
		m.poller.jevFinishAttended = map[string]string{}
	}
	m.poller.jevFinishAttended[sess.ID] = jevFinishIdentity(sess)
	m.poller.mu.Unlock()
}

func (m *Model) jevFinishAttended(sess store.Session) bool {
	return m.mode == modeFocus && m.focusedID == sess.ID || m.jevFinish.attended[sess.ID] == jevFinishIdentity(sess)
}

// checkFinishedWithJev asks about one finished row per poll pass, off the
// event loop. One call out at a time keeps a board of many finishes from
// fanning out a request per row at once.
func (m *Model) checkFinishedWithJev() tea.Cmd {
	key, _ := m.jevKey()
	if !m.jevFinishCheck || key == "" || m.jevFinish.busy || m.conversation == nil {
		return nil
	}
	sess, identity, ok := m.jevFinishCandidate()
	if !ok {
		return nil
	}
	if m.jevFinish.checked == nil {
		m.jevFinish.checked = map[string]string{}
	}
	m.jevFinish.checked[sess.ID] = identity
	m.jevFinish.busy = true
	locator := search.NewLocator(m.conversation.locator.ClaudeHome, m.conversation.locator.CodexRoot)
	tool := historyToolFormats(m.cfg)[sess.Tool]
	database := opencodeDBPath()
	return func() tea.Msg {
		msg := jevFinishResultMsg{id: sess.ID, identity: identity}
		target, found := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID)
		if !found {
			return msg
		}
		messages, err := search.ReadMessages(target, database)
		if err != nil {
			return msg
		}
		if msg.probe = jevFinishProbeReply(messages); msg.probe != jevProbeNone {
			msg.ok = true
			return msg
		}
		state := jevFinishTurns(messages)
		if len(state) == 0 {
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), jevFinishTimeout)
		defer cancel()
		msg.verdict, msg.ok = askJevFinish(ctx, jevHTTPClient, jevEndpoint, key, state)
		return msg
	}
}

// jevFinishTurns is the latest user and assistant turns, oldest first.
func jevFinishTurns(messages []search.Message) []suggestMessage {
	var out []suggestMessage
	for i := len(messages) - 1; i >= 0 && len(out) < jevFinishMessages; i-- {
		message := messages[i]
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if text := boundedText(message.Text, jevFinishMessageChars); text != "" {
			out = append([]suggestMessage{{Role: message.Role, Text: text}}, out...)
		}
	}
	return out
}

// askJevFinish posts both noul questions in one call against one state.
func askJevFinish(ctx context.Context, client *http.Client, endpoint, key string, state []suggestMessage) (jevFinishVerdict, bool) {
	answers, ok := askJevNoul(ctx, client, endpoint, key, map[string]any{"messages": state}, map[string]string{
		"finished":       jevFinishedQuestion,
		"missing_chrome": jevMissingChromeQuestion,
	})
	if !ok {
		return jevFinishVerdict{}, false
	}
	return jevFinishVerdict{finished: answers["finished"], missingChrome: answers["missing_chrome"]}, true
}

func askJevNoul(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]string) (map[string]float64, bool) {
	return jev.AskNoul(ctx, client, endpoint, key, state, questions)
}

// jevFinishAction is what a verdict calls for.
type jevFinishAction int

const (
	jevFinishNone jevFinishAction = iota
	jevFinishDone
	jevFinishRelaunchChrome
	jevFinishProbeAgent
)

// decide reads a verdict. Missing Chrome acts only when the work is also
// confidently not done: a session that finished its job without a browser
// has nothing to continue.
func (v jevFinishVerdict) decide() jevFinishAction {
	switch {
	case v.missingChrome >= jevFinishBandHigh && v.finished <= jevFinishBandLow:
		return jevFinishRelaunchChrome
	case v.finished >= jevFinishBandHigh && v.missingChrome <= jevFinishBandLow:
		return jevFinishDone
	case v.finished >= jevFinishProbeLow && v.missingChrome <= jevFinishBandLow:
		return jevFinishProbeAgent
	}
	return jevFinishNone
}

func (m *Model) applyJevFinish(msg jevFinishResultMsg) {
	m.jevFinish.busy = false
	if !msg.ok || !m.jevFinishCheck {
		return
	}
	sess, ok := m.sessionByID(msg.id)
	// The row moved on while JEV read it: the verdict is about a finish that
	// is no longer the one on screen.
	if !ok || sess.Archived || sess.Status != status.Finished || jevFinishIdentity(sess) != msg.identity || m.jevFinishAttended(sess) {
		return
	}
	if msg.probe != jevProbeNone {
		logging.Info("jev finish probe answered", "session", sess.ID, "done", msg.probe == jevProbeDone)
		if msg.probe == jevProbeDone {
			m.reportDone(fmt.Sprintf("%s says it is done", sess.Name))
		}
		return
	}
	logging.Info("jev finish check", "session", sess.ID, "finished", msg.verdict.finished, "missing_chrome", msg.verdict.missingChrome)
	switch msg.verdict.decide() {
	case jevFinishDone:
		m.reportDone(fmt.Sprintf("JEV says %s is done (%.1f%%)", sess.Name, msg.verdict.finished*100))
	case jevFinishProbeAgent:
		// The poller types it once the prompt is at rest and nobody is
		// typing there, so it cannot land on a draft or a dialog.
		if err := m.store.QueuePendingInput(sess.ID, queuedJevFinishProbe(sess)); err != nil {
			m.errBar.text = "JEV finish probe: " + err.Error()
		}
	case jevFinishRelaunchChrome:
		if m.jevFinish.chromeRelaunched[sess.ID] {
			m.errBar.text = fmt.Sprintf("JEV: %s still lacks Chrome after a relaunch with %s; left as it is", sess.Name, chromeFlag)
			return
		}
		if err := m.relaunchWithChrome(sess); err != nil {
			m.errBar.text = "JEV Chrome relaunch: " + err.Error()
			return
		}
		if m.jevFinish.chromeRelaunched == nil {
			m.jevFinish.chromeRelaunched = map[string]bool{}
		}
		m.jevFinish.chromeRelaunched[sess.ID] = true
		m.reportDone(fmt.Sprintf("JEV: %s lacked Chrome (%.1f%%); relaunched with %s and told it to continue",
			sess.Name, msg.verdict.missingChrome*100, chromeFlag))
	}
}

// relaunchWithChrome ends the session's pane and resumes the same
// conversation, on the same model, with Chrome attached and the continue
// prompt that unpark sends a session stopped mid-turn.
func (m *Model) relaunchWithChrome(sess store.Session) error {
	tool, ok := m.cfg.Tools[sess.Tool]
	if !ok {
		return fmt.Errorf("tool %s is no longer configured", sess.Tool)
	}
	if !isDir(sess.Cwd) {
		return fmt.Errorf("working directory no longer exists: %s", sess.Cwd)
	}
	if sess.AgentSessionID == "" || tool.ResumeByIDCommand == "" {
		return fmt.Errorf("%s has no conversation id to resume", sess.Name)
	}
	revive, err := launch.ReviveCommand(sess.Tool, tool, sess.AgentSessionID, sess.Model)
	if err != nil {
		return err
	}
	revive = withChromeFlag(revive)
	const prompt = "continue"
	base := launch.WithPrompt(tool, revive, prompt)
	if err := m.killSession(sess); err != nil {
		return err
	}
	bind := func() error {
		launchedAt := time.Now()
		if err := m.store.SetAgentLaunchedAt(sess.ID, launchedAt); err != nil {
			return err
		}
		m.bindReviveLocally(sess.ID, launchedAt)
		return nil
	}
	if err := m.relaunchSession(sess, tool, base, status.Starting, bind); err != nil {
		return err
	}
	if launch.TypesPrompt(tool, prompt) {
		if err := m.store.QueuePendingInput(sess.ID, prompt); err != nil {
			return err
		}
	}
	m.rebuildRows()
	return nil
}

// withChromeFlag adds --chrome to a command that does not already carry it.
func withChromeFlag(command string) string {
	for _, field := range strings.Fields(command) {
		if field == chromeFlag {
			return command
		}
	}
	return command + " " + chromeFlag
}

func queuedJevFinishProbe(sess store.Session) string {
	return jevFinishProbePrefix + sess.LastStatusAt.UTC().Format(time.RFC3339Nano)
}

func isJevFinishProbe(input string) bool {
	return input == jevFinishProbe || strings.HasPrefix(input, jevFinishProbePrefix)
}

func (p *poller) jevFinishProbeCurrent(sess store.Session, capture tmux.Capture, derived string) (bool, error) {
	current, err := p.store.Get(sess.ID)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	attended := p.jevFinishFocusedID == sess.ID || p.jevFinishAttended[sess.ID] == jevFinishIdentity(current)
	inputAt := p.operatorInputAt[sess.ID]
	p.mu.Unlock()
	return !current.Archived && current.Status == status.Finished && derived == status.Finished &&
		sess.PendingInputs[0] == queuedJevFinishProbe(current) && !attended &&
		!current.OperatorAt.After(current.LastStatusAt) && !inputAt.After(current.LastStatusAt) &&
		!capture.State.InputAt.After(current.LastStatusAt), nil
}

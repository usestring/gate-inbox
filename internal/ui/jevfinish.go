package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The JEV finish check reads a Claude session's transcript each time the row
// turns finished and asks JEV two yes-no questions about it in one call:
// whether the work is done, and whether the agent stopped because it had no
// Chrome browser tools. The second has a fixed remedy, so the board applies
// it without asking: end the pane, resume the same conversation with
// --chrome, and tell the agent to continue.
//
// Only a confident answer acts. Anything inside the band, a missing key, a
// failed request or an unreadable transcript leaves the row as it was.

const jevFinishCheckSetting = "experimental_jev_finish_check"

const (
	jevFinishBandLow  = 0.20
	jevFinishBandHigh = 0.80
	// jevFinishMessages is how many of the latest turns the state carries,
	// each cut to jevFinishMessageChars.
	jevFinishMessages     = 6
	jevFinishMessageChars = 1500
	jevFinishTimeout      = 6 * time.Second
	chromeFlag            = "--chrome"
)

const (
	jevFinishedQuestion = "Has the agent delivered what the user asked for in this session, with no work left open, " +
		"nothing pending on the user, and no blocker stopping it?"
	jevMissingChromeQuestion = "Did the agent stop, or fail to do what was asked, because it had no Chrome browser control: " +
		"the Claude in Chrome tools (mcp__claude-in-chrome__*) were missing or unavailable, the browser extension was not " +
		"connected, or it said the session must be started with --chrome?"
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
	body, err := json.Marshal(struct {
		Model     string `json:"model"`
		State     any    `json:"state"`
		Questions any    `json:"questions"`
	}{
		Model: jevModel,
		State: map[string]any{"messages": state},
		Questions: map[string]any{
			"finished":       map[string]any{"type": "noul", "instructions": jevFinishedQuestion},
			"missing_chrome": map[string]any{"type": "noul", "instructions": jevMissingChromeQuestion},
		},
	})
	if err != nil {
		return jevFinishVerdict{}, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return jevFinishVerdict{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return jevFinishVerdict{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jevFinishVerdict{}, false
	}
	var reply struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply) != nil || reply.Model != jevModel {
		return jevFinishVerdict{}, false
	}
	probability := func(name string) (float64, bool) {
		answer, ok := reply.Answers[name]
		if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			return 0, false
		}
		return *answer.Noul, true
	}
	finished, okFinished := probability("finished")
	chrome, okChrome := probability("missing_chrome")
	if !okFinished || !okChrome {
		return jevFinishVerdict{}, false
	}
	return jevFinishVerdict{finished: finished, missingChrome: chrome}, true
}

// jevFinishAction is what a verdict calls for.
type jevFinishAction int

const (
	jevFinishNone jevFinishAction = iota
	jevFinishDone
	jevFinishRelaunchChrome
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
	logging.Info("jev finish check", "session", sess.ID, "finished", msg.verdict.finished, "missing_chrome", msg.verdict.missingChrome)
	switch msg.verdict.decide() {
	case jevFinishDone:
		m.reportDone(fmt.Sprintf("JEV says %s is done (%.1f%%)", sess.Name, msg.verdict.finished*100))
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

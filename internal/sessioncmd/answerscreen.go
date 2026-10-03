package sessioncmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Answering the dialogs that are not questions.
//
// A permission prompt, a workspace-trust dialog and an MCP-server dialog ask
// whether the child may do something on the user's machine. They are the
// user's to answer and never an agent's, and until now the only way to answer
// one was a person at the child's pane. That left a parent with a child it
// could not move, and an operator handed keystrokes for a session they never
// started.
//
// So answer_session answers them, on one condition: the answer is the user's
// own, relayed. The parent asks its user with its own question tool, copying
// the dialog's text and its choices word for word, and Gate Inbox reads the
// parent's transcript for that dialog and the user's answer before keying
// anything (the evidence rules are relay.go's). The choice is found by its
// text, never by a number, and the marker is seen on it before Enter; after
// Enter the dialog is seen to clear.
//
// Whatever this cannot read as choices -- a screen ending in a legend over no
// choices it knows -- takes keys, under the same relay: the parent asks its
// user with the screen's lines quoted, offering the keys as the options, and
// the keys are sent only when the user's answer spells them.

// AnswerKeys sends keys, relayed from the caller's user, to the screen its
// child targetID is holding: the path for a dialog answer_session cannot read
// as choices. relay must be true; see relay.go and keysAllowed.
func (s *Sessions) AnswerKeys(sessionID, targetID string, keys []string, relay bool) (AnsweredQuestion, error) {
	if len(keys) == 0 {
		return AnsweredQuestion{}, errors.New("keys is empty; give the key names your user chose, such as Down, Enter or 2")
	}
	if len(keys) > maxKeys {
		return AnsweredQuestion{}, fmt.Errorf("keys holds %d keys; at most %d are sent in one call", len(keys), maxKeys)
	}
	for _, key := range keys {
		if !keysAllowed.MatchString(key) {
			return AnsweredQuestion{}, fmt.Errorf("%q is not a key this sends; use Up, Down, Left, Right, Enter, "+
				"Tab, BTab, Escape, Space, BSpace, or one letter or digit", key)
		}
	}
	runtime, err := s.open()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return AnsweredQuestion{}, err
	}
	target, err := runtime.child(caller, targetID)
	if err != nil {
		return AnsweredQuestion{}, err
	}
	pane := tmuxPane{driver: runtime.driver, id: target.ID}
	raw, err := pane.Capture()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	shown := screenLines(ansi.Strip(raw))
	spelled := strings.Join(keys, " ")
	guard := s.guard(runtime.store, caller, target, relay)
	if !relay {
		return AnsweredQuestion{}, fmt.Errorf("%w: keys are sent only as your user's own choice. Ask your user with "+
			"your own question tool, quoting session %s's screen word for word:\n\n%s\n\nwith the keys to press "+
			"as the options (for example %q), then call answer_session with those keys and relay: true",
			errScreenNeedsRelay, target.ID, shown, spelled)
	}
	if err := guard.admitKeys(shown, spelled); err != nil {
		return AnsweredQuestion{}, fmt.Errorf("session %s: %w", target.ID, err)
	}
	answered, err := sendKeys(pane, raw, keys)
	guard.finish(err)
	answered.SessionID, answered.Name = target.ID, target.Name
	if err != nil {
		return AnsweredQuestion{}, fmt.Errorf("session %s: %w", target.ID, err)
	}
	logging.Info("parent sent its user's keys to a child's screen", "parent", caller.ID, "session", target.ID, "keys", spelled)
	return answered, nil
}

// maxKeys bounds one keys call: a choice is a few arrows and Enter.
const maxKeys = 12

// keysAllowed is the keys answer_session sends: navigation, Enter, Escape,
// and one printable letter or digit. Nothing that types a command.
var keysAllowed = regexp.MustCompile(`^(?:Up|Down|Left|Right|Enter|Tab|BTab|Escape|Space|BSpace|[0-9A-Za-z])$`)

var errScreenNeedsRelay = errors.New("this dialog is your user's to answer")

// screenLines is the text a keys relay is checked against: the held dialog
// as ReadScreen reads it, or else the pane's last lines.
func screenLines(plain string) string {
	if screen, ok := dialog.ReadScreen(plain); ok {
		return logging.ScrubWrapped(screen.Text())
	}
	lines := strings.Split(strings.TrimRight(plain, " \n"), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < 8; i-- {
		line := strings.TrimRight(lines[i], " ")
		if strings.TrimSpace(line) == "" || strings.Trim(strings.TrimSpace(line), "─━╌") == "" {
			continue
		}
		kept = append([]string{line}, kept...)
	}
	return logging.ScrubWrapped(strings.Join(kept, "\n"))
}

// sendKeys sends keys and reads back that the screen changed.
func sendKeys(pane dialogPane, raw string, keys []string) (AnsweredQuestion, error) {
	answered := AnsweredQuestion{Question: screenLines(ansi.Strip(raw)), Answer: strings.Join(keys, " ")}
	before := dialog.Compact(ansi.Strip(raw))
	if err := pane.Keys(keys...); err != nil {
		return answered, err
	}
	if _, err := waitFor(pane, func(raw string) bool { return dialog.Compact(ansi.Strip(raw)) != before }); err != nil {
		return answered, fmt.Errorf("the keys %s were sent but the screen did not change; read_session shows it", answered.Answer)
	}
	answered.Changed = true
	return answered, nil
}

// answerScreen answers the permission, trust or other numbered dialog screen
// is, on pane, with the choice its label names, behind guard.
func (r *runtime) answerScreen(target store.Session, pane dialogPane, screen dialog.Screen, reply, by, byID string, guard *answerGuard) (AnsweredQuestion, error) {
	if guard != nil && !guard.relay {
		return AnsweredQuestion{}, wrapped(dialog.ErrNotKeyAnswerable,
			fmt.Sprintf("session %s: %v", target.ID, screenRelayRefusal(target, screen)))
	}
	n := screen.Choose(reply)
	if n == 0 {
		return AnsweredQuestion{}, wrapped(dialog.ErrNotKeyAnswerable, fmt.Sprintf("session %s is on a %s, and "+
			"%q names none of its choices (%s); answer with the text of the one your user chose",
			target.ID, screen.Kind, reply, quoteAll(screen.Labels())))
	}
	if guard != nil {
		if err := guard.admitScreen(screen, n); err != nil {
			return AnsweredQuestion{}, fmt.Errorf("session %s: %w", target.ID, err)
		}
	}
	answered, err := pickChoice(pane, screen, n, target.Tool)
	guard.finish(err)
	answered.SessionID, answered.Name = target.ID, target.Name
	if err != nil {
		logging.Warn(by+"'s choice on a child's dialog did not land", by, byID, "session", target.ID, "err", err)
		return AnsweredQuestion{}, fmt.Errorf("session %s: %w", target.ID, err)
	}
	logging.Info(by+" answered a child's "+string(screen.Kind), by, byID, "session", target.ID, "choice", answered.Selected)
	return answered, nil
}

// screenRelayRefusal tells a parent exactly how to put a dialog it may not
// answer itself to its user.
func screenRelayRefusal(target store.Session, screen dialog.Screen) error {
	return fmt.Errorf("%w: session %s is on a %s, and whether to allow it is your user's call, never an "+
		"agent's, so answer_session keys it only with relay: true. Ask your user with your own question tool, "+
		"copying the question %q and the options %s word for word, then call answer_session on session %s "+
		"with answer set to the option they chose and relay: true",
		errScreenNeedsRelay, target.ID, screen.Kind, logging.ScrubWrapped(screen.Prompt()),
		quoteAll(scrubAll(screen.Labels())), target.ID)
}

// pickChoice moves the marker to choice n, sees it there, presses Enter and
// sees the dialog clear.
func pickChoice(pane dialogPane, screen dialog.Screen, n int, tools ...string) (AnsweredQuestion, error) {
	read := dialog.ReadScreen
	if len(tools) > 0 {
		read = func(raw string) (dialog.Screen, bool) { return dialog.ReadScreenFor(tools[0], raw) }
	}
	label := screen.Choices[n-1].Label
	answered := AnsweredQuestion{Question: screen.Prompt(), Answer: label, Selected: label}
	identity := screen.Identity()
	at := screen.Cursor()
	switch {
	case at < 0 && screen.Choices[n-1].Number > 0:
		// No marker to count from, but the dialog takes the choice's own digit;
		// the digit is read off the row whose text was named.
		if err := pane.Keys(fmt.Sprint(screen.Choices[n-1].Number)); err != nil {
			return answered, err
		}
	case at < 0:
		return answered, errors.New("cannot tell which choice the marker is on, so nothing was keyed; read it again in a moment")
	default:
		keys := dialog.SelectKeys(at+1, n)
		if strings.ContainsRune(screen.Legend, rune(0x21c6)) {
			for i, key := range keys {
				if key == "Down" {
					keys[i] = "Right"
				}
				if key == "Up" {
					keys[i] = "Left"
				}
			}
		}
		if len(keys) > 1 {
			if err := pane.Keys(keys[:len(keys)-1]...); err != nil {
				return answered, err
			}
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := read(raw)
			return ok && moved.Identity() == identity && moved.Cursor() == n-1
		}); err != nil {
			return answered, fmt.Errorf("%w: the marker never reached %q, so Enter was not pressed", errDialogMoved, label)
		}
		if err := pane.Keys("Enter"); err != nil {
			return answered, err
		}
	}
	if _, err := waitForLong(pane, func(raw string) bool {
		now, ok := read(raw)
		return !ok || now.Identity() != identity
	}); err != nil {
		return answered, fmt.Errorf("%q was chosen but the dialog is still standing; read_session shows it", label)
	}
	answered.Verified = true
	return answered, nil
}

// waitForLong is waitFor over the readback timeout: a permission prompt is
// taken down when the harness gets to it, which can be after the tool starts.
func waitForLong(pane dialogPane, done func(raw string) bool) (string, error) {
	deadline := time.Now().Add(readbackTimeout)
	for {
		raw, err := pane.Capture()
		if err != nil {
			return "", err
		}
		if done(raw) {
			return raw, nil
		}
		if time.Now().After(deadline) {
			return raw, errors.New("timed out waiting for the pane to redraw")
		}
		time.Sleep(settlePoll)
	}
}

// admitScreen checks that choice n of screen is the caller's user's own
// answer and records it, before any key is sent. The user's dialog must ask
// the screen's text word for word -- inside a longer question is fine -- with
// its choices as the options, and the user's answer must be choice n.
func (g *answerGuard) admitScreen(screen dialog.Screen, n int) error {
	prompt := screen.Prompt()
	labels := screen.Labels()
	choice := labels[n-1]
	hash := screenHash(prompt, labels)
	evidence, err := g.evidence(g.since(), hash,
		func(q convo.AskQuestion) bool { return asksScreen(q, prompt, labels) },
		func(given string) bool { return sameText(given, choice) },
		choice,
		fmt.Sprintf("no dialog of yours asks this %s word for word with its choices as the options; ask your "+
			"user with your own question tool, copying the question %q and the options %s, then answer with "+
			"their choice and relay: true", screen.Kind, logging.ScrubWrapped(prompt), quoteAll(scrubAll(labels))))
	if err != nil {
		return err
	}
	return g.record(hash, choice, evidence)
}

// admitKeys checks that the caller's user chose exactly spelled, asked with
// shown quoted, and records it.
func (g *answerGuard) admitKeys(shown, spelled string) error {
	hash := screenHash(shown, []string{"keys"})
	evidence, err := g.evidence(g.since(), hash,
		func(q convo.AskQuestion) bool { return quotes(q.Question, shown) },
		func(given string) bool {
			return sameText(strings.TrimPrefix(strings.ToLower(normalise(given)), "keys:"), spelled)
		},
		spelled,
		"no dialog of yours quotes the child's screen word for word; ask your user with your own question tool, "+
			"quoting the screen read_session shows and offering the keys as the options, then relay their choice")
	if err != nil {
		return err
	}
	return g.record(hash, spelled, evidence)
}

// record writes one relayed answer's ledger row, pending.
func (g *answerGuard) record(hash, answer, evidence string) error {
	id, err := g.store.RecordAnswer(store.DialogAnswer{
		TargetSession:     g.target.ID,
		QuestionHash:      hash,
		Answer:            answer,
		BySession:         g.caller.ID,
		Mode:              store.AnswerRelayedUser,
		EvidenceToolUseID: evidence,
	})
	if errors.Is(err, store.ErrEvidenceUsed) {
		return fmt.Errorf("%w: your user's answer to that dialog has already been relayed once; ask them again", errRelayRefused)
	}
	if err != nil {
		return fmt.Errorf("cannot record the answer before keying it, so nothing was keyed: %w", err)
	}
	g.rows = append(g.rows, id)
	return nil
}

// since is when the child's dialog went up, as near as can be told: when it
// last turned waiting, or when its agent launched. A relayed answer must come
// after it.
func (g *answerGuard) since() time.Time {
	if g.target.Status == status.Waiting && !g.target.LastStatusAt.IsZero() {
		return g.target.LastStatusAt
	}
	return g.target.LaunchTime()
}

// asksScreen is the parent's question quoting prompt with labels as its
// options, compared as dialog.Compact so a word the pane broke across two
// rows still matches. A dialog with more choices than AskUserQuestion takes
// (four) matches on any four of them.
func asksScreen(q convo.AskQuestion, prompt string, labels []string) bool {
	if !quotes(q.Question, prompt) || len(q.Options) == 0 {
		return false
	}
	if len(labels) <= 4 && len(q.Options) != len(labels) {
		return false
	}
	for i, option := range q.Options {
		if len(labels) <= 4 {
			if !sameText(option.Label, labels[i]) {
				return false
			}
			continue
		}
		if !containsText(labels, option.Label) {
			return false
		}
	}
	return true
}

// quotes is text holding shown word for word, raw or with credentials
// scrubbed the way the relay showed it.
func quotes(text, shown string) bool {
	have := dialog.Compact(text)
	for _, want := range []string{dialog.Compact(shown), dialog.Compact(logging.ScrubWrapped(shown))} {
		if want != "" && strings.Contains(have, want) {
			return true
		}
	}
	return false
}

// sameText is a and b equal as dialog.Compact, raw or scrubbed.
func sameText(a, b string) bool {
	return dialog.Compact(a) != "" && (dialog.Compact(a) == dialog.Compact(b) ||
		dialog.Compact(a) == dialog.Compact(logging.ScrubWrapped(b)))
}

func containsText(labels []string, label string) bool {
	for _, l := range labels {
		if sameText(label, l) {
			return true
		}
	}
	return false
}

func screenHash(prompt string, labels []string) string {
	sum := sha256.New()
	sum.Write([]byte("screen\x00" + dialog.Compact(prompt)))
	for _, label := range labels {
		sum.Write([]byte{0})
		sum.Write([]byte(dialog.Compact(label)))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func quoteAll(labels []string) string {
	quoted := make([]string, len(labels))
	for i, label := range labels {
		quoted[i] = fmt.Sprintf("%q", label)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func scrubAll(labels []string) []string {
	out := make([]string, len(labels))
	for i, label := range labels {
		out[i] = logging.ScrubWrapped(label)
	}
	return out
}

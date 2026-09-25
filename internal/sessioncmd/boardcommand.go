package sessioncmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A board extension typing a tool's own command into a session.
//
// Send is a message: queued, fenced as the extension's, and typed in when
// the session next rests. A tool's own command is not a message. "/model
// fast" fenced as somebody's words is text the agent reads rather than a
// command the CLI runs, and the only way to change what a running CLI is
// doing from outside it is to type the command at its prompt, as the
// operator would. So it is typed now, unfenced, or refused -- never queued
// to land later on a pane that may be showing something else by then.

// maxCommandLen bounds a command. A tool's commands are short; a long line
// is prose, which is Send's to carry.
const maxCommandLen = 256

// confirmWait bounds the watch for a command's own confirmation, and
// confirmPoll paces it. Vars so a test can shrink them.
var (
	confirmWait = 2 * time.Second
	confirmPoll = 50 * time.Millisecond
)

// selectedRow matches a numbered menu row with the selection marker on it:
// the row Enter would pick. The number is required, so a prompt's own
// marker with the command echoed after it is not a menu row.
var selectedRow = regexp.MustCompile(`(?m)^[ \x{A0}]*[\x{276F}\x{203A}][ \x{A0}]+\d+\.[ \x{A0}]+(.+?)[ \x{A0}]*$`)

// BoardCommand types text, one of a tool's own commands, into an agent
// session's input line and submits it, on behalf of the board. It is
// refused unless the session rests at its prompt with nothing written there
// and nobody typing, and for a tool whose prompt the board cannot find.
// With confirm set it then presses Enter on the command's own confirmation,
// if one is drawn whose selected row begins with confirm.
func (s *Sessions) BoardCommand(ctx context.Context, targetID, text, confirm string) (err error) {
	defer start("sessioncmd.board.command", sessionAttr(targetID)).done(&err)
	text = strings.TrimSpace(text)
	confirm = strings.TrimSpace(confirm)
	switch {
	case text == "":
		return errors.New("the command is empty")
	case strings.ContainsAny(text, "\r\n"):
		return errors.New("a command is one line; send prose with Send")
	case !strings.HasPrefix(text, "/"):
		return fmt.Errorf("%q is not a tool command, which starts with \"/\"; send prose with Send", text)
	case len(text) > maxCommandLen:
		return fmt.Errorf("the command is %d bytes; a tool command is at most %d", len(text), maxCommandLen)
	}
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return err
	}
	if err := runtime.running(target); err != nil {
		return err
	}
	engine, err := status.NewEngine(runtime.cfg)
	if err != nil {
		return err
	}
	pane, err := runtime.driver.CapturePane(target.ID)
	if err != nil {
		return err
	}
	clean := ansi.Strip(pane)
	if err := runtime.promptHold(engine, target, clean); err != nil {
		return err
	}
	before := confirmRows(clean, confirm)
	if err := runtime.driver.SendText(target.ID, text); err != nil {
		return err
	}
	if confirm == "" {
		return nil
	}
	return runtime.confirm(ctx, engine, target, confirm, before)
}

// promptHold is why a running session cannot be typed a command now,
// wrapping extension.ErrNoCommandLine or extension.ErrNotAtPrompt, or nil
// when it rests at its prompt with nothing written there and nobody typing.
// clean is its live pane with the escapes stripped.
func (r *runtime) promptHold(engine *status.Engine, target store.Session, clean string) error {
	if r.cfg.Tools[target.Tool].ActivityCutoff == "" {
		return fmt.Errorf("session %s: tool %q declares no activity_cutoff: %w", target.ID, target.Tool, extension.ErrNoCommandLine)
	}
	if engine.ViewportDisplaced(target.Tool, clean) {
		return fmt.Errorf("session %s is scrolled into its history; Unpark it first: %w", target.ID, extension.ErrNotAtPrompt)
	}
	if hold := engine.TypingHold(target.Tool, clean); hold != "" {
		return fmt.Errorf("session %s is %s: %w", target.ID, hold, extension.ErrNotAtPrompt)
	}
	if at, err := r.driver.SessionInputAt(target.ID); err == nil && !at.IsZero() && time.Since(at) < status.OperatorQuiet {
		return fmt.Errorf("someone is typing in session %s: %w", target.ID, extension.ErrNotAtPrompt)
	}
	if x, y, err := r.driver.Cursor(target.ID); err == nil && engine.DraftInComposer(target.Tool, clean, x, y) {
		return fmt.Errorf("someone has text written at session %s's prompt: %w", target.ID, extension.ErrNotAtPrompt)
	}
	return nil
}

// confirm watches the pane after a command for its own confirmation and
// presses Enter on it once. before is how many matching rows the pane
// showed before the command was typed, so a menu left on the screen by an
// earlier command is not taken for this one's.
//
// It is not a dialog answerer. A pane showing anything but a numbered menu
// whose selected row begins with confirm is only read, so a permission
// prompt, a question, or a menu with its selection somewhere else is never
// sent a key.
func (r *runtime) confirm(ctx context.Context, engine *status.Engine, target store.Session, confirm string, before int) error {
	deadline := time.Now().Add(confirmWait)
	pressed := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(confirmPoll):
		}
		pane, err := r.driver.CapturePane(target.ID)
		if err != nil {
			return err
		}
		clean := ansi.Strip(pane)
		showing := confirmRows(clean, confirm) > before
		if held, ok := dialog.Inspect(clean); ok && held.Guarded() {
			showing = false
		}
		switch {
		case showing && !pressed:
			if err := r.driver.SendKeys(target.ID, "Enter"); err != nil {
				return err
			}
			pressed = true
		case !showing && pressed:
			return nil
		}
		if time.Now().After(deadline) {
			if pressed {
				return fmt.Errorf("session %s: the confirmation beginning %q was still on the pane %s after Enter: %w", target.ID, confirm, confirmWait, extension.ErrCommandHeld)
			}
			if hold := engine.TypingHold(target.Tool, clean); hold != "" {
				return fmt.Errorf("session %s is %s after the command, on nothing this answers: %w", target.ID, hold, extension.ErrCommandHeld)
			}
			return nil
		}
	}
}

// confirmRows counts the selected menu rows beginning with confirm.
func confirmRows(pane, confirm string) int {
	if confirm == "" {
		return 0
	}
	n := 0
	for _, match := range selectedRow.FindAllStringSubmatch(pane, -1) {
		if len(match[1]) >= len(confirm) && strings.EqualFold(match[1][:len(confirm)], confirm) {
			n++
		}
	}
	return n
}

// BoardUnpark presses the tool's jump-back key when an agent session's
// viewport is parked above the live bottom, and reports whether it was.
func (s *Sessions) BoardUnpark(targetID string) (parked bool, err error) {
	defer start("sessioncmd.board.unpark", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return false, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return false, err
	}
	if err := runtime.running(target); err != nil {
		return false, err
	}
	engine, err := status.NewEngine(runtime.cfg)
	if err != nil {
		return false, err
	}
	pane, err := runtime.driver.CapturePane(target.ID)
	if err != nil {
		return false, err
	}
	if !engine.ViewportDisplaced(target.Tool, ansi.Strip(pane)) {
		return false, nil
	}
	key := engine.JumpToBottomKey(target.Tool)
	if key == "" {
		return true, fmt.Errorf("session %s is scrolled into its history and tool %q declares no jump_to_bottom_key to bring it back", target.ID, target.Tool)
	}
	return true, runtime.driver.SendKeys(target.ID, key)
}

// running refuses a row with no live pane to type into.
func (r *runtime) running(target store.Session) error {
	if target.Archived {
		return fmt.Errorf("session %s is archived; restore it with %s first", target.ID, r.words.Restore)
	}
	if !r.driver.Exists(target.ID) {
		return fmt.Errorf("session %s is not running; revive it with %s first", target.ID, r.words.Revive)
	}
	return nil
}

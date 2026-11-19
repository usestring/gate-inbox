package ui

import "time"

// interruptKeyGap separates the keys of a multi-key interrupt_keys sequence.
// OpenCode arms its interrupt on one Escape and fires on the next, but two
// Escapes written in one burst reach its input parser as a single
// "\x1b\x1b", which it reads as alt+Escape rather than two presses.
const interruptKeyGap = 150 * time.Millisecond

// keySender is the slice of the tmux driver sendInterruptKeys needs.
type keySender interface {
	SendKeys(id string, keys ...string) error
}

// sendInterruptKeys sends a tool's interrupt_keys one key per write, apart by
// interruptKeyGap, so each is read as its own press. A single key goes at once.
func sendInterruptKeys(t keySender, id string, keys []string) error {
	for i, key := range keys {
		if i > 0 {
			time.Sleep(interruptKeyGap)
		}
		if err := t.SendKeys(id, key); err != nil {
			return err
		}
	}
	return nil
}

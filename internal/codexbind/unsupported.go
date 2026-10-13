//go:build !darwin && !linux

package codexbind

import (
	"context"
	"errors"
)

const Env = "GATE_INBOX_CALLER_BINDING"

type Identity struct{ Home, Socket, Pane string }

func (Identity) Live() bool      { return false }
func (Identity) PanePin() string { return "" }
func Run(context.Context, string, string, []string, bool) error {
	return errors.New("Codex caller binding requires macOS or Linux Unix peer credentials")
}
func Resolve(context.Context, string, string, bool) (Identity, error) {
	return Identity{}, errors.New("Codex caller binding is unsupported on this platform")
}
func Interrupt(context.Context, string, string) (Identity, error) {
	return Identity{}, errors.New("Codex caller binding is unsupported on this platform")
}

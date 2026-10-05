package tmux

import (
	"strings"

	"github.com/usestring/gate-inbox/internal/logging"
)

// The back-to-board keys.
//
// C-q and C-\ are bound in the root table to detach the client when the
// pane is the board's, and to pass the key through otherwise. A board
// session is one named gi_*. An adopted pane is somebody else's, in a
// session somebody else attaches to from their own terminal, where a
// detach would take their terminal away rather than bring anybody back to
// the board. So it takes two things there:
//
//   - The pane carries the user option @gi_row, the row it is on the board
//     as, set when the board adopts it and cleared when it lets it go.
//   - The client is one the board attached: AttachCommand records its name
//     in the session's @gi_client, and the board clears it on return.
//
// Both are user options, which tmux itself never reads and which no
// config can collide with. In every other pane and every other client the
// binding sends the key on, as if it were not bound.

// RowOption is the pane option naming the board row an adopted pane is.
const RowOption = "@gi_row"

// clientOption is the session option naming the client the board attached.
const clientOption = "@gi_client"

// backCondition is true where the back key means back to the board.
const backCondition = "#{||:#{m:" + prefix + "*,#{session_name}},#{&&:#{" + RowOption + "},#{==:#{" + clientOption + "},#{client_name}}}}"

// backBindings are the root-table bindings of the back keys.
func backBindings() [][]string {
	return [][]string{
		{"bind-key", "-n", "C-q", "if-shell", "-F", backCondition, "detach-client", "send-keys C-q"},
		{"bind-key", "-n", `C-\`, "if-shell", "-F", backCondition, "detach-client", `send-keys C-\\`},
	}
}

// ownBackBinding reports whether a list-keys line is a back-key binding
// this program installed, in this build's form or an earlier one's.
func ownBackBinding(line string) bool {
	return strings.Contains(line, "detach-client") && strings.Contains(line, "m:"+prefix+"*")
}

// MarkAdopted labels an adopted pane with its row, which is what gives it
// the back keys, and offers those keys to the pane's server. On a server
// the board does not own they are bound only where nothing else holds the
// key, as on a shared one.
func (d *Driver) MarkAdopted(id string) error {
	target, ok := d.AdoptedTarget(id)
	if !ok {
		return nil
	}
	if _, err := d.output([]string{"-L", target.Socket, "set-option", "-p", "-t", target.Name, RowOption, id}); err != nil {
		return err
	}
	return d.ensureBackKeysOn(target.Socket)
}

// unmarkAdopted takes the label off a pane the board is letting go. The
// pane may be gone already, which leaves nothing to clear.
func (d *Driver) unmarkAdopted(id string) {
	target, ok := d.AdoptedTarget(id)
	if !ok {
		return
	}
	if _, err := d.output([]string{"-L", target.Socket, "set-option", "-p", "-u", "-t", target.Name, RowOption}); err != nil {
		logging.Debug("adopted pane label not cleared", "session", id, logging.Err(err))
	}
}

// ensureBackKeysOn binds the back keys on socket once per driver, through
// EnsureBindings on the board's own server.
func (d *Driver) ensureBackKeysOn(socket string) error {
	d.adoptedMu.Lock()
	done := d.backKeysOn[socket]
	if d.backKeysOn == nil {
		d.backKeysOn = map[string]bool{}
	}
	d.backKeysOn[socket] = true
	d.adoptedMu.Unlock()
	if done {
		return nil
	}
	if socket == d.socket {
		return d.EnsureBindings()
	}
	binds, err := d.sharedBindings(socket, backBindings())
	if err != nil || len(binds) == 0 {
		return err
	}
	_, err = d.output(append([]string{"-L", socket}, commandList(binds...)...))
	return err
}

// backKeyOn is the back key this program holds on socket, as the operator
// reads it, or empty when the operator's own config holds both.
func (d *Driver) backKeyOn(socket string) string {
	bound, running, err := d.rootKeys(socket)
	if err != nil || !running {
		return ""
	}
	for _, key := range []string{"C-q", `C-\`} {
		if line, ok := bound[key]; ok && ownBackBinding(line) && strings.Contains(line, RowOption) {
			return "Ctrl+" + strings.TrimPrefix(key, "C-")
		}
	}
	return ""
}

// AttachDone clears the record of the client the board attached to an
// adopted pane's session, so a client that later takes the same name is
// not taken for the board's.
func (d *Driver) AttachDone(id string) {
	target, ok := d.AdoptedTarget(id)
	if !ok {
		return
	}
	session, _, _ := d.attachTarget(target)
	if _, err := d.output([]string{"-L", target.Socket, "set-option", "-u", "-t", "=" + session + ":", clientOption}); err != nil {
		logging.Debug("attached client record not cleared", "session", id, logging.Err(err))
	}
}

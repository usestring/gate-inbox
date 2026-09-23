package tmux

// The pipe is how the manager talks to a pane it is watching closely.
//
// Every read here used to be a forked tmux process, at ~5ms for a capture and
// ~3ms for a send-keys. Over the pooled control pipe a send is ~1µs, so the
// keystroke path a focused pane depends on stays on the pipe.
//
// Captures do not: tmux retains a control client's command replies without
// limit (tmux/tmux#5553), and a capture reply is a screen of text, so every
// capture-pane forks instead. See the note above CaptureScrollback.
//
// The client is the one the poll pass already holds: one per tmux server,
// attached to the manager's own anchor session. That matters more than the
// speed. The mirror this replaces was a client of the session it watched,
// and on 2026-08-27 one per focus change exhausted the operator's tmux
// server and destroyed every agent running on it. A client of the anchor is
// a client of a session the manager created, which startControl enforces
// structurally -- so there is no version of this path that can repeat that
// outage, whatever a caller asks for.
//
// Every entry point falls back to a fork. A server with no usable client is
// slow, not broken, and captureClients already backs such a server off.

// pipeFor resolves a session to the pooled control client for its server.
// ok is false when this server has no usable client, which is the caller's
// cue to fork instead.
func (d *Driver) pipeFor(id string) (control *Control, ok bool) {
	control, err := d.captures.client(d, d.TargetFor(id).Socket)
	if err != nil || control == nil {
		return nil, false
	}
	return control, true
}

// pipeCommand runs one tmux command for a session over the pooled pipe.
// A command that failed on a live pipe is the command's own failure, not
// the pipe's, so it is reported rather than retried by fork: a fork would
// fail the same way and cost a process to say so.
func (d *Driver) pipeCommand(id, command string) (string, bool) {
	control, ok := d.pipeFor(id)
	if !ok {
		return "", false
	}
	out, err := control.Command(command)
	if err != nil {
		return "", false
	}
	return out, true
}

// PipeSend forwards one tmux command over the pooled pipe without waiting
// for its reply, and reports whether the write went out.
//
// True means the pane owns the command from that moment, acknowledged or
// not, so a caller that resent on a slow reply would send it twice. False
// means nothing went out -- no client, or a failed write -- which is the one
// case a forked resend is safe.
func (d *Driver) PipeSend(id, command string) bool {
	control, ok := d.pipeFor(id)
	if !ok {
		return false
	}
	return control.Send(command) == nil
}

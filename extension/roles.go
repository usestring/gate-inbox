package extension

import "context"

// RoleProvider is implemented by an extension whose roles, the ones it
// passes as LaunchRequest.Role, ask the board to treat those sessions
// differently from an ordinary child.
//
// A role is otherwise only a tag. What a spec can change is fixed here, as
// flags the board reads, rather than callbacks into the board: the board
// asks about a role on every pass and every drawn row, and a flag is an
// answer that costs nothing and cannot hang or panic.
//
// Roles is read once, when the host collects the extensions' hooks. Each
// Name is qualified with the extension's ID, as the launched session's role
// is, so one extension's spec never applies to another's sessions.
type RoleProvider interface {
	Roles() []RoleSpec
}

// RoleSpec is how the board treats the sessions launched with one role.
// The zero RoleSpec is an ordinary child.
type RoleSpec struct {
	// Name is the role, unqualified, as passed in LaunchRequest.Role.
	Name string
	// OnScreen keeps the session in view: it is never folded away with its
	// parent's children, it is not counted in the parent's child badge, and
	// triage queues it like a top-level session instead of skipping it as a
	// subagent. It is for a helper that is there to be looked at, such as
	// one holding a question for the operator.
	OnScreen bool
	// FloatParent moves the top-level block holding the session it is filed
	// under to the head of its group in the board's list, for as long as
	// the session is unarchived.
	FloatParent bool
	// Silent keeps its waits and rests from being relayed to the session it
	// was filed under or spawned by: the extension that launched it is
	// already watching it, and the parent hearing about it too is noise.
	Silent bool
	// SkipSendChildren leaves it out of its parent's send_children, which
	// reports it as skipped with a reason. A helper the board launched is
	// not part of the fan-out the parent is instructing.
	SkipSendChildren bool
	// RelayToParent makes its send_session to the session it is filed under
	// (its ParentID) the operator's own words: queued as the operator's,
	// without the cross-session envelope, after the provider's Relayer, if
	// it has one, has vetted or rewritten it. A session with the role that
	// has been archived is refused, since whatever it was relaying has been
	// overtaken.
	RelayToParent bool
	// PinnedStatus makes the board read the session's status from its
	// status file whatever its tool's status source, and never mark it as
	// missing its hooks. The extension pins the status with
	// BoardHost.PinStatus; a relayed send releases the pin, removing the
	// file and setting the row working.
	PinnedStatus bool
}

// Relayer is optionally implemented by a RoleProvider to vet or rewrite a
// relayed send: a send_session from one of its RelayToParent sessions to
// the session it is filed under.
//
// deliver is what is queued, as the operator's words. An empty deliver with
// a nil error queues nothing and the send still reports success: the relay
// was an instruction to the extension alone. An error, or a panic, refuses
// the send, and the error is what the sender reads.
type Relayer interface {
	Relay(ctx context.Context, relay Relay) (deliver string, err error)
}

// Relay is one relayed send.
type Relay struct {
	// From is the session with the role; To is the session it is filed
	// under.
	From SessionInfo
	To   SessionInfo
	// Text is what From sent.
	Text string
}

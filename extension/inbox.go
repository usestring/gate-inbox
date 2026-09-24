package extension

import "time"

// SenderOperator is QueuedMessage.From for a message the operator sent from
// a shell rather than a session.
const SenderOperator = "operator"

// SenderRelayed is QueuedMessage.From for the operator's words forwarded to
// the session on their behalf by an extension, rather than typed at a
// shell.
const SenderRelayed = "operator/relayed"

// SenderExtension is QueuedMessage.From for a message the board extension id
// queued through BoardHost.Send.
func SenderExtension(id string) string { return "extension/" + id }

// MessageFilter narrows Board.Messages. The zero value is the newest
// messages from every sender, delivered or not, up to the host's default.
type MessageFilter struct {
	// From keeps one sender's messages: a session id, SenderOperator,
	// SenderRelayed, or SenderExtension of an extension's id.
	From string
	// Pending keeps only messages still waiting when true, only delivered
	// ones when false, and both when nil.
	Pending *bool
	// Limit caps the messages returned; zero takes the host's default.
	Limit int
}

// QueuedMessage is one message sent to a session.
type QueuedMessage struct {
	ID int64
	// From is who sent it, in MessageFilter.From's terms.
	From string
	// Subject is the label a later message from the same sender on the same
	// subject replaces this one under while it waits; empty when unlabelled.
	Subject  string
	Text     string
	QueuedAt time.Time
	// DeliveredAt is when it was typed into the session, or zero while it
	// is still waiting.
	DeliveredAt time.Time
}

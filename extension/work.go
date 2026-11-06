package extension

import (
	"context"
	"errors"
)

// WorkItem is something a session made that belongs among its work rows on
// the board, beside the pull requests and tickets the board finds itself:
// a page it published, today. The board shows it under the session, with
// its title, and opens URL when the row is opened. It survives a restart.
type WorkItem struct {
	// Kind is what the item is. WorkArtifact is the only kind so far.
	Kind WorkKind
	// ID is the extension's own name for the item, such as the id its
	// service gave a page. Attaching an item with an ID the session already
	// has from this extension replaces that one's URL and title, which is
	// what a republish wants. Empty takes URL.
	ID string
	// Title is what the row says. Empty shows the ID.
	Title string
	// URL is where the item lives: an absolute http or https address.
	URL string
}

// WorkKind is a kind of WorkItem.
type WorkKind string

// WorkArtifact is a page or document the session published.
const WorkArtifact WorkKind = "artifact"

// WorkService attaches work to the session a Host acts as.
type WorkService interface {
	// Attach records item as the calling session's work. It is refused for
	// a Host acting as an operator's shell, which is no session.
	Attach(ctx context.Context, item WorkItem) error
}

// WorkHost is implemented by a Host that can attach work to its session:
// the one the board lends a session's extensions. Call AttachWork rather
// than asserting it, so an extension also builds against a Host that
// cannot.
type WorkHost interface {
	Work() WorkService
}

// ErrWorkUnsupported is AttachWork on a Host that cannot attach work, such
// as an extension's own test fake, or a nil one.
var ErrWorkUnsupported = errors.New("this host cannot attach work to a session")

// AttachWork attaches item to the session host acts as, or fails with
// ErrWorkUnsupported when host cannot.
func AttachWork(ctx context.Context, host Host, item WorkItem) error {
	if host == nil {
		return ErrWorkUnsupported
	}
	worker, ok := host.(WorkHost)
	if !ok {
		return ErrWorkUnsupported
	}
	work := worker.Work()
	if work == nil {
		return ErrWorkUnsupported
	}
	return work.Attach(ctx, item)
}

package extensionhost

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
)

var _ extension.WorkHost = (*Host)(nil)

// maxWorkTitle bounds a work item's title, in bytes: it is one row's label,
// and a page's whole text passed by mistake must not land in the state
// database.
const maxWorkTitle = 200

// Work is the calling session's work rows, filed under the extension the
// Host is lent to.
func (h *Host) Work() extension.WorkService {
	return work{sessions: h.sessions, owner: h.owner}
}

type work struct {
	sessions *sessions
	owner    string
}

func (w work) Attach(ctx context.Context, item extension.WorkItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.sessions.operator || w.sessions.callerFor(ctx) == "" {
		return errors.New("an operator's shell is no session, so there is nothing to attach work to")
	}
	if w.owner == "" {
		return errors.New("this Host is lent to no extension, so there is nobody to file the work under")
	}
	stored, err := sessionWork(w.owner, item)
	if err != nil {
		return err
	}
	return w.sessions.cmds.AttachWork(w.sessions.callerFor(ctx), stored)
}

// sessionWork checks item and turns it into the row the store keeps.
func sessionWork(owner string, item extension.WorkItem) (store.SessionWork, error) {
	if item.Kind != extension.WorkArtifact {
		return store.SessionWork{}, fmt.Errorf("work kind %q is not one the board shows; use %q", item.Kind, extension.WorkArtifact)
	}
	address := strings.TrimSpace(item.URL)
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return store.SessionWork{}, fmt.Errorf("work URL %q is not an absolute http or https address", item.URL)
	}
	id := strings.TrimSpace(item.ID)
	if id == "" {
		id = address
	}
	title := strings.Join(strings.Fields(item.Title), " ")
	if len(title) > maxWorkTitle {
		title = strings.ToValidUTF8(title[:maxWorkTitle], "")
	}
	return store.SessionWork{Source: owner, Kind: string(item.Kind), ID: id, URL: address, Title: title}, nil
}

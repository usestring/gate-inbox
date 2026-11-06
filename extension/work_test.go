package extension_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// workHost is a Host that can attach work, lent per extension as the board's is, recording what
// each extension attached under its own ID.
type workHost struct {
	extension.Host
	owner    string
	attached *[]string
}

func (h workHost) ForExtension(id string) extension.Host {
	return workHost{owner: id, attached: h.attached}
}
func (h workHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
func (h workHost) Work() extension.WorkService {
	return workRecorder(func(item extension.WorkItem) {
		*h.attached = append(*h.attached, h.owner+" "+item.Title+" "+item.URL)
	})
}

type workRecorder func(extension.WorkItem)

func (r workRecorder) Attach(_ context.Context, item extension.WorkItem) error {
	r(item)
	return nil
}

// The Host an extension is lent through the registry still attaches work, under that extension's
// own ID, even though the registry wraps it to tag its log.
func TestAttachWorkReachesTheHostThroughTheRegistry(t *testing.T) {
	var attached []string
	pages := &hostTaker{stub: stub{id: "pages"}}
	if _, err := mustRegistry(t, pages).RegisterMCP(newServer(), extension.SessionContext{SessionID: "s1", Host: workHost{attached: &attached}}, nil); err != nil {
		t.Fatal(err)
	}
	item := extension.WorkItem{Kind: extension.WorkArtifact, Title: "Report", URL: "https://pages.example.test/p/1"}
	if err := extension.AttachWork(context.Background(), pages.got, item); err != nil {
		t.Fatal(err)
	}
	if len(attached) != 1 || attached[0] != "pages Report https://pages.example.test/p/1" {
		t.Fatalf("attached %q", attached)
	}
}

// A Host that cannot attach work -- an extension's own fake, or none -- says so rather than
// dropping the item, and it says so through the registry's wrapper too.
func TestAttachWorkOnAHostThatCannot(t *testing.T) {
	item := extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p/1"}
	if err := extension.AttachWork(context.Background(), nil, item); !errors.Is(err, extension.ErrWorkUnsupported) {
		t.Errorf("nil host: %v", err)
	}
	plain := &hostTaker{stub: stub{id: "plain"}}
	if _, err := mustRegistry(t, plain).RegisterMCP(newServer(), extension.SessionContext{Host: scopedHost{}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := extension.AttachWork(context.Background(), plain.got, item); !errors.Is(err, extension.ErrWorkUnsupported) {
		t.Errorf("host without work, through the registry: %v", err)
	}
}

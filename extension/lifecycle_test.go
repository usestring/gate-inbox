package extension_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// boardStub is an extension that runs on the board, and records what the
// registry did with it into log.
type boardStub struct {
	id         string
	off        bool
	fail       error
	panic      bool
	stopPanics bool
	log        *[]string
}

func (s *boardStub) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: s.id, Version: "1"}
}
func (s *boardStub) Configure(extension.Config) error { return nil }
func (s *boardStub) Enabled() bool                    { return !s.off }

func (s *boardStub) StartBoard(ctx context.Context, board extension.BoardHost) (func(), error) {
	*s.log = append(*s.log, "start "+s.id+" in "+board.ConfigDir())
	board.Subscribe(func(extension.StatusEvent) {})
	if s.panic {
		panic("boom")
	}
	return func() {
		*s.log = append(*s.log, "stop "+s.id)
		if s.stopPanics {
			panic("stop boom")
		}
	}, s.fail
}

// fakeBoard counts the subscriptions each extension's view holds.
type fakeBoard struct {
	live map[string]int
	log  *[]string
}

type fakeView struct {
	extension.Board
	board *fakeBoard
	id    string
}

func (v fakeView) ConfigDir() string { return "/config" }
func (v fakeView) Subscribe(func(extension.StatusEvent)) func() {
	v.board.live[v.id]++
	return func() {}
}
func (v fakeView) OnPass(func(extension.Pass)) func() { return func() {} }

func (b *fakeBoard) hostFor(id string) (extension.BoardHost, func()) {
	return fakeView{board: b, id: id}, func() {
		*b.log = append(*b.log, "release "+id)
		b.live[id] = 0
	}
}

func TestStartBoardStartsEnabledProvidersAndStopsThemInReverse(t *testing.T) {
	var log []string
	registry := mustRegistry(t,
		&boardStub{id: "first", log: &log},
		&boardStub{id: "off", off: true, log: &log},
		&stub{id: "tools-only"},
		&boardStub{id: "second", log: &log},
	)
	board := &fakeBoard{live: map[string]int{}, log: &log}
	results, stop, err := registry.StartBoard(context.Background(), board.hostFor)
	if err != nil {
		t.Fatal(err)
	}
	if ids := resultIDs(results); !slices.Equal(ids, []string{"first", "second"}) {
		t.Fatalf("asked %v, want the enabled providers in order", ids)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"start first in /config", "start second in /config",
		"stop second", "release second", "stop first", "release first",
	}
	if !slices.Equal(log, want) {
		t.Fatalf("log = %q, want %q", log, want)
	}
}

func TestStartBoardReleasesAFailedProviderAndCarriesOn(t *testing.T) {
	var log []string
	registry := mustRegistry(t,
		&boardStub{id: "errs", fail: errors.New("no"), log: &log},
		&boardStub{id: "panics", panic: true, log: &log},
		&boardStub{id: "fine", log: &log},
	)
	board := &fakeBoard{live: map[string]int{}, log: &log}
	results, stop, err := registry.StartBoard(context.Background(), board.hostFor)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Err == nil || results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "panicked while starting: boom") {
		t.Fatalf("results = %+v, want the first two failed", results)
	}
	if results[2].Err != nil {
		t.Fatalf("the healthy provider failed: %v", results[2].Err)
	}
	if board.live["errs"] != 0 || board.live["panics"] != 0 || board.live["fine"] != 1 {
		t.Fatalf("live subscriptions = %v, want only the healthy provider's", board.live)
	}
	log = nil
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log, []string{"stop fine", "release fine"}) {
		t.Fatalf("stop ran %q, want only the provider that started", log)
	}
}

func TestStartBoardReportsAPanickingStopAndStopsTheRest(t *testing.T) {
	var log []string
	registry := mustRegistry(t,
		&boardStub{id: "first", log: &log},
		&boardStub{id: "second", stopPanics: true, log: &log},
	)
	board := &fakeBoard{live: map[string]int{}, log: &log}
	_, stop, err := registry.StartBoard(context.Background(), board.hostFor)
	if err != nil {
		t.Fatal(err)
	}
	err = stop()
	if err == nil || !strings.Contains(err.Error(), `extension "second": panicked while stopping: stop boom`) {
		t.Fatalf("stop = %v, want the panic reported", err)
	}
	if !slices.Contains(log, "stop first") || !slices.Contains(log, "release second") {
		t.Fatalf("log = %q, want every provider stopped and released", log)
	}
}

func TestStartBoardNeedsAConfiguredRegistry(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&boardStub{id: "x", log: new([]string)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.StartBoard(context.Background(), (&fakeBoard{live: map[string]int{}, log: new([]string)}).hostFor); err == nil {
		t.Fatal("an unconfigured registry started its providers")
	}
}

func TestKindOf(t *testing.T) {
	for to, want := range map[string]extension.EventKind{
		"finished": extension.EventStop, "waiting": extension.EventAsk, "errored": extension.EventError,
		"working": "", "idle": "", "dead": "", "starting": "",
	} {
		if got := extension.KindOf(to); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", to, got, want)
		}
	}
}

func resultIDs(results []extension.BoardResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	return ids
}

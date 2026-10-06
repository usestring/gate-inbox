package ui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/promptname"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/store"
)

// A session is named from outside it. The agent is never asked to rename
// itself: a directive typed into its conversation costs a turn of the session's
// own model, lands in its transcript, and is ignored often enough that the
// board still had to fall back. Instead the board names each row itself, in
// this order:
//
//   - the tool's own title, kebab-cased whole, once it is at most five words;
//   - otherwise a small model's name for the session's first prompt, cut to
//     its first thousand characters and run out of process;
//   - otherwise, when no model answers, the title squeezed by sessname.Kebab.

// sessionNamer names a session from its opening prompt.
type sessionNamer interface {
	Name(ctx context.Context, prompt string) (string, error)
}

// newSessionNamer is the namer a board starts with. Tests replace it so no
// test spends a model call.
var newSessionNamer = func() sessionNamer { return promptname.New(nil) }

// promptNamesPerPass bounds the model calls one naming pass makes, and
// promptNameWorkers how many run at once. A board adopting eighty panes at
// start-up names them over several passes rather than forking eighty CLIs.
const (
	promptNamesPerPass = 8
	promptNameWorkers  = 4
)

// promptNamedMsg is a launch's name, picked as soon as the session started.
type promptNamedMsg struct {
	id      string
	renamed []renamedSession
	err     error
}

// askNamer runs the namer over prompts, keyed by session id, and returns the
// names it answered with. A call that fails is only absent: the caller falls
// back to the title.
func askNamer(namer sessionNamer, prompts map[string]string) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, promptNameWorkers)
	for id, prompt := range prompts {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			name, err := namer.Name(context.Background(), prompt)
			if err != nil || name == "" {
				return
			}
			mu.Lock()
			out[id] = name
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// nameLaunch names a session the board just started from the prompt it was
// started with, without waiting for the next pass. It is a no-op without a
// namer or a prompt, and the row keeps its placeholder until the pass names it
// from its title.
func (m *Model) nameLaunch(id, prompt string) tea.Cmd {
	if m.namer == nil || m.store == nil || id == "" || prompt == "" {
		return nil
	}
	if m.promptNamed == nil {
		m.promptNamed = map[string]bool{}
	}
	m.promptNamed[id] = true
	taken := map[string]bool{}
	for _, sess := range m.sessions {
		if sess.ID != id {
			taken[sess.Name] = true
		}
	}
	namer, stor := m.namer, m.store
	return func() tea.Msg {
		name, err := namer.Name(context.Background(), prompt)
		if err != nil {
			return promptNamedMsg{id: id, err: err}
		}
		names := sessname.Assign(sessname.Exact{}, []sessname.Entry{{ID: id, Title: name}}, taken)
		if names[id] == "" {
			return promptNamedMsg{id: id}
		}
		ok, err := stor.AutoRenameSession(id, names[id], store.SourcePrompt)
		if err != nil || !ok {
			return promptNamedMsg{id: id, err: err}
		}
		return promptNamedMsg{id: id, renamed: []renamedSession{{id: id, name: names[id], source: store.SourcePrompt}}}
	}
}

package sessioncmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/store"
)

// HostSession is the part of a session shown in another board's browser.
type HostSession struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

// HostSnapshot is the worker's response to a remote refresh.
type HostSnapshot struct {
	Sessions []HostSession `json:"sessions"`
	Matched  int           `json:"matched"`
}

// HostSessions is the worker's session snapshot. Opening the database
// read-only keeps a repeated remote snapshot off the board's write path.
func (s *Sessions) HostSessions() (HostSnapshot, error) {
	worker, err := s.openHost()
	if err != nil {
		return HostSnapshot{}, err
	}
	defer worker.store.Close()
	list, err := worker.list("", ListOptions{Limit: MaxSessionLimit})
	if err != nil {
		return HostSnapshot{}, err
	}
	items := make([]HostSession, 0, len(list.Sessions))
	for _, session := range list.Sessions {
		items = append(items, HostSession{
			ID: session.ID, Name: session.Name, Status: session.Status, Running: session.Running,
		})
	}
	return HostSnapshot{Sessions: items, Matched: list.Matched}, nil
}

func (s *Sessions) openHost() (*runtime, error) {
	// Check for a board before LoadDir, which would write a default config
	// when SSH reached the wrong Gate Inbox home.
	if _, err := os.Stat(s.statePath()); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(s.configDir, "config.toml")); err != nil {
		return nil, err
	}
	return s.openOn(func() (*store.Store, error) {
		return store.OpenReadOnly(s.statePath())
	})
}

// AttachHost opens a worker's existing agent pane on this process's terminal.
func (s *Sessions) AttachHost(id string) error {
	worker, err := s.openHost()
	if err != nil {
		return err
	}
	defer worker.store.Close()
	target, err := worker.agent(id)
	if err != nil {
		return err
	}
	if err := worker.reach(target); err != nil {
		return err
	}
	if target.Archived || !worker.driver.Exists(target.ID) {
		return fmt.Errorf("session %s has no live pane", target.ID)
	}

	// A sizing failure can leave the pane narrow but must not prevent access.
	_ = worker.driver.PrepareAttach(target.ID)
	defer worker.driver.AttachDone(target.ID)
	cmd := worker.driver.AttachCommand(target.ID)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

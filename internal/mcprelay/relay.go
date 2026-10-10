// Package mcprelay is the MCP server a claude started outside the board
// carries in every session, so that once the board adopts its pane it has the
// same tools a launched session has, without a restart.
//
// Claude Code starts the servers in its user config with every session, shows
// a server that fails or exits as failed, and never restarts a stdio server
// that exited. So the relay starts cleanly everywhere, lists no tools, and
// stays up for the session's whole life. When the pane it runs in is adopted
// it starts `gate-inbox mcp` for the adopted row as a worker behind it, pipes
// the session to it, and tells Claude Code its tools changed; when the pane is
// let go, or the worker exits, it goes back to listing none. A worker started
// later is whatever binary is installed then, so the tools update with Gate
// Inbox while the relay itself stays the same process.
package mcprelay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

// Worker is the `gate-inbox mcp` process behind a live relay.
type Worker interface {
	// Send writes one JSON-RPC message, newline included, to the worker.
	Send(line []byte) error
	// Lines yields what the worker writes, a message per line, and is closed
	// when the worker exits.
	Lines() <-chan []byte
	// Stop ends the worker. Lines is closed once it has.
	Stop()
}

// Relay is one claude session's server.
type Relay struct {
	In  io.Reader
	Out io.Writer
	// Caller names the board row this session is, when its pane is adopted.
	Caller func() (string, bool)
	// Start runs a worker for the row id.
	Start func(id string) (Worker, error)
	// Upgraded reports that the installed binary changed since the worker
	// started, so the next idle moment restarts it on the new one.
	Upgraded func() bool
	// Every is how often adoption is looked at again.
	Every   time.Duration
	Version string
}

const relayIDPrefix = "gate-inbox-relay-"

// message is the part of a JSON-RPC message the relay routes on.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (m message) request() bool { return m.Method != "" && len(m.ID) > 0 }

type state struct {
	r Relay
	// init is the client's initialize params, replayed to every worker.
	init json.RawMessage
	// ready is set once the client has finished the handshake; list_changed
	// sent before it would arrive at a client that has not asked for tools.
	ready  bool
	worker Worker
	lines  <-chan []byte
	id     string
	// pending is every client request the worker has not answered yet, by
	// raw id, so a worker that dies answers them all rather than leaving
	// the session waiting on a call that will never return.
	pending map[string]json.RawMessage
	// handshake is the id of the worker initialize still unanswered.
	handshake string
	// buffered holds client messages that arrive while the worker is still
	// answering its initialize.
	buffered [][]byte
	seq      int
	upgrade  bool
	// announced is set while the client has been told of the worker's
	// tools, so it is told again only when they go.
	announced bool
	// wait is how many checks to skip before starting a worker again after
	// one exited on its own, and backoff what the next exit will make it,
	// so a worker that cannot start is not restarted every tick.
	wait, backoff int
}

// Run serves until the client closes In.
func (r Relay) Run() error {
	input := make(chan []byte)
	go func() {
		defer close(input)
		scanner := bufio.NewScanner(r.In)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			input <- bytes.Clone(scanner.Bytes())
		}
	}()
	every := r.Every
	if every <= 0 {
		every = 2 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	s := &state{r: r, pending: map[string]json.RawMessage{}}
	defer s.stop()
	for {
		select {
		case line, ok := <-input:
			if !ok {
				return nil
			}
			if err := s.fromClient(line); err != nil {
				return err
			}
		case line, ok := <-s.lines:
			if !ok {
				if err := s.workerGone("the Gate Inbox worker exited"); err != nil {
					return err
				}
				continue
			}
			if err := s.fromWorker(line); err != nil {
				return err
			}
		case <-ticker.C:
			if err := s.check(); err != nil {
				return err
			}
		}
	}
}

func (s *state) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.r.Out.Write(append(data, '\n'))
	return err
}

func (s *state) result(id json.RawMessage, result any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *state) fail(id json.RawMessage, text string) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": text}})
}

func (s *state) toolsChanged() error {
	if !s.ready {
		return nil
	}
	s.announced = s.worker != nil && s.handshake == ""
	return s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
}

func (s *state) fromClient(line []byte) error {
	var msg message
	if json.Unmarshal(line, &msg) != nil {
		return nil
	}
	switch msg.Method {
	case "initialize":
		s.init = msg.Params
		if err := s.result(msg.ID, s.initializeResult()); err != nil {
			return err
		}
		return nil
	case "notifications/initialized":
		s.ready = true
		return s.check()
	}
	if s.worker == nil {
		return s.idle(msg)
	}
	if s.handshake != "" {
		s.buffered = append(s.buffered, line)
		return nil
	}
	return s.forward(msg, line)
}

func (s *state) forward(msg message, line []byte) error {
	if msg.request() {
		s.pending[string(msg.ID)] = msg.ID
	}
	if err := s.worker.Send(append(line, '\n')); err != nil {
		return s.workerGone("the Gate Inbox worker stopped taking requests")
	}
	return nil
}

// idle answers what a session with no worker asks.
func (s *state) idle(msg message) error {
	if !msg.request() {
		return nil
	}
	switch msg.Method {
	case "tools/list":
		return s.result(msg.ID, map[string]any{"tools": []any{}})
	case "ping":
		return s.result(msg.ID, map[string]any{})
	}
	return s.fail(msg.ID, "this session is not on the Gate Inbox board")
}

func (s *state) initializeResult() map[string]any {
	version := "2025-06-18"
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(s.init, &params) == nil && params.ProtocolVersion != "" {
		version = params.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
		"serverInfo":      map[string]any{"name": "gate-inbox", "version": s.r.Version},
	}
}

func (s *state) fromWorker(line []byte) error {
	var msg message
	if json.Unmarshal(line, &msg) == nil && msg.Method == "" && len(msg.ID) > 0 {
		if string(msg.ID) == s.handshake {
			return s.handshaken()
		}
		var own string
		if json.Unmarshal(msg.ID, &own) == nil && len(own) > len(relayIDPrefix) && own[:len(relayIDPrefix)] == relayIDPrefix {
			return nil
		}
		delete(s.pending, string(msg.ID))
	}
	if s.handshake != "" {
		return nil
	}
	_, err := s.r.Out.Write(append(line, '\n'))
	if err == nil && s.upgrade && len(s.pending) == 0 {
		return s.restart()
	}
	return err
}

// handshaken finishes a worker's start: the client is told its tools
// changed, and whatever it sent meanwhile goes through.
func (s *state) handshaken() error {
	s.handshake = ""
	s.healthy()
	if err := s.worker.Send([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		return s.workerGone("the Gate Inbox worker stopped taking requests")
	}
	if err := s.toolsChanged(); err != nil {
		return err
	}
	buffered := s.buffered
	s.buffered = nil
	for _, line := range buffered {
		var msg message
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if s.worker == nil {
			if err := s.idle(msg); err != nil {
				return err
			}
			continue
		}
		if err := s.forward(msg, line); err != nil {
			return err
		}
	}
	return nil
}

// check starts, stops or restarts the worker to match the pane.
func (s *state) check() error {
	if !s.ready || s.init == nil {
		return nil
	}
	id, adopted := s.r.Caller()
	if s.worker != nil {
		switch {
		case !adopted || id != s.id:
			if err := s.dropped("this session left the Gate Inbox board"); err != nil {
				return err
			}
		case s.r.Upgraded != nil && s.r.Upgraded():
			s.upgrade = true
			if len(s.pending) == 0 && s.handshake == "" {
				return s.restart()
			}
			return nil
		default:
			return nil
		}
	}
	if !adopted {
		s.wait, s.backoff = 0, 0
		return nil
	}
	if s.wait > 0 {
		s.wait--
		return nil
	}
	return s.start(id)
}

func (s *state) start(id string) error {
	worker, err := s.r.Start(id)
	if err != nil {
		s.backoff = min(max(2*s.backoff, 1), maxBackoff)
		s.wait = s.backoff
		return nil
	}
	s.worker, s.lines, s.id, s.upgrade = worker, worker.Lines(), id, false
	s.seq++
	handshake, _ := json.Marshal(relayIDPrefix + "init-" + strconv.Itoa(s.seq))
	s.handshake = string(handshake)
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(handshake),
		"method": "initialize", "params": s.init})
	if err := worker.Send(append(request, '\n')); err != nil {
		return s.workerGone("the Gate Inbox worker did not start")
	}
	return nil
}

// restart moves the session to a worker on the binary installed now. The
// new worker's handshake announces its tools, which may differ.
func (s *state) restart() error {
	id := s.id
	if err := s.drop("the Gate Inbox worker restarted on a new version"); err != nil {
		return err
	}
	if err := s.start(id); err != nil {
		return err
	}
	if s.worker == nil && s.announced {
		return s.toolsChanged()
	}
	return nil
}

// drop stops the worker and answers everything it still owed.
func (s *state) drop(reason string) error {
	s.stop()
	for key, id := range s.pending {
		delete(s.pending, key)
		if err := s.fail(id, reason); err != nil {
			return err
		}
	}
	buffered := s.buffered
	s.buffered = nil
	for _, line := range buffered {
		var msg message
		if json.Unmarshal(line, &msg) == nil {
			if err := s.idle(msg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *state) workerGone(reason string) error {
	s.backoff = min(max(2*s.backoff, 1), maxBackoff)
	s.wait = s.backoff
	return s.dropped(reason)
}

// dropped is drop for a worker whose tools may have been announced.
func (s *state) dropped(reason string) error {
	announced := s.announced
	if err := s.drop(reason); err != nil {
		return err
	}
	if !announced {
		return nil
	}
	return s.toolsChanged()
}

// maxBackoff caps the checks skipped after a worker exits: about a minute at
// the default interval.
const maxBackoff = 32

func (s *state) stop() {
	if s.worker != nil {
		s.worker.Stop()
	}
	s.worker, s.lines, s.id, s.handshake, s.upgrade = nil, nil, "", "", false
}

// healthy resets the backoff once a worker has answered its handshake.
func (s *state) healthy() { s.wait, s.backoff = 0, 0 }

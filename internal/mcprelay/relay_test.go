package mcprelay

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWorker is a worker the test answers for.
type fakeWorker struct {
	got   chan string
	lines chan []byte
	once  sync.Once
}

func newFakeWorker() *fakeWorker {
	return &fakeWorker{got: make(chan string, 64), lines: make(chan []byte, 64)}
}

func (w *fakeWorker) Send(line []byte) error {
	w.got <- strings.TrimSpace(string(line))
	return nil
}
func (w *fakeWorker) Lines() <-chan []byte { return w.lines }
func (w *fakeWorker) Stop()                { w.once.Do(func() { close(w.lines) }) }
func (w *fakeWorker) say(line string)      { w.lines <- []byte(line) }

func (w *fakeWorker) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case line := <-w.got:
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("worker got %q: %v", line, err)
		}
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("the worker was sent nothing")
		return nil
	}
}

// rig is a relay with its client's ends of stdin and stdout.
type rig struct {
	t       *testing.T
	in      *io.PipeWriter
	out     *bufio.Scanner
	adopted atomic.Value
	starts  chan *fakeWorker
	fail    atomic.Bool
	upgrade atomic.Bool
	done    chan error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &rig{t: t, in: inW, out: bufio.NewScanner(outR), starts: make(chan *fakeWorker, 8), done: make(chan error, 1)}
	r.adopted.Store("")
	relay := Relay{
		In: inR, Out: outW, Every: 10 * time.Millisecond, Version: "test",
		Caller: func() (string, bool) {
			id := r.adopted.Load().(string)
			return id, id != ""
		},
		Start: func(id string) (Worker, error) {
			if r.fail.Load() {
				r.starts <- nil
				return nil, errors.New("no binary")
			}
			w := newFakeWorker()
			r.starts <- w
			return w, nil
		},
		Upgraded: func() bool { return r.upgrade.Load() },
	}
	go func() { r.done <- relay.Run(); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-r.done:
		case <-time.After(3 * time.Second):
			t.Error("the relay did not end when its client closed stdin")
		}
	})
	return r
}

func (r *rig) send(line string) {
	r.t.Helper()
	if _, err := r.in.Write([]byte(line + "\n")); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read() map[string]any {
	r.t.Helper()
	got := make(chan string, 1)
	go func() {
		if r.out.Scan() {
			got <- r.out.Text()
		}
		close(got)
	}()
	select {
	case line, ok := <-got:
		if !ok {
			r.t.Fatal("the relay closed its output")
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			r.t.Fatalf("relay wrote %q: %v", line, err)
		}
		return msg
	case <-time.After(3 * time.Second):
		r.t.Fatal("the relay wrote nothing")
		return nil
	}
}

func (r *rig) worker() *fakeWorker {
	r.t.Helper()
	select {
	case w := <-r.starts:
		return w
	case <-time.After(3 * time.Second):
		r.t.Fatal("no worker was started")
		return nil
	}
}

func (r *rig) handshake() {
	r.t.Helper()
	r.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2"}}}`)
	result := r.read()["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		r.t.Fatalf("initialize answered %v", result)
	}
	caps := result["capabilities"].(map[string]any)["tools"].(map[string]any)
	if caps["listChanged"] != true {
		r.t.Fatalf("the relay does not say its tools can change: %v", result)
	}
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// live adopts the pane and finishes the worker's handshake, answering for the
// worker.
func (r *rig) live(id string) *fakeWorker {
	r.t.Helper()
	r.adopted.Store(id)
	w := r.worker()
	init := w.next(r.t)
	if init["method"] != "initialize" || init["params"].(map[string]any)["clientInfo"].(map[string]any)["name"] != "claude-code" {
		r.t.Fatalf("the worker's initialize was not the client's: %v", init)
	}
	raw, _ := json.Marshal(init["id"])
	w.say(`{"jsonrpc":"2.0","id":` + string(raw) + `,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"gate-inbox","version":"2"}}}`)
	if got := w.next(r.t); got["method"] != "notifications/initialized" {
		r.t.Fatalf("the worker was not told the handshake finished: %v", got)
	}
	if got := r.read(); got["method"] != "notifications/tools/list_changed" {
		r.t.Fatalf("the client was not told its tools changed: %v", got)
	}
	return w
}

func TestARelayOffTheBoardListsNoToolsAndStaysUp(t *testing.T) {
	r := newRig(t)
	r.handshake()
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if tools := r.read()["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("tools off the board = %v", tools)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_session"}}`)
	if msg := r.read(); msg["error"] == nil || float64(2) != msg["id"] {
		t.Fatalf("a call off the board = %v", msg)
	}
	r.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if msg := r.read(); msg["id"] != "p" || msg["result"] == nil {
		t.Fatalf("ping = %v", msg)
	}
}

func TestAdoptionStartsAWorkerAndTheClientReachesIt(t *testing.T) {
	r := newRig(t)
	r.handshake()
	w := r.live("a1b2c3d4")
	r.send(`{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	if got := w.next(t); got["method"] != "tools/list" || got["id"] != float64(5) {
		t.Fatalf("the worker got %v", got)
	}
	w.say(`{"jsonrpc":"2.0","id":5,"result":{"tools":[{"name":"send_session"}]}}`)
	if got := r.read(); got["id"] != float64(5) || !strings.Contains(mustJSON(got), "send_session") {
		t.Fatalf("the client got %v", got)
	}
	// A request the worker makes, and the client's answer, pass straight
	// through.
	w.say(`{"jsonrpc":"2.0","id":"w1","method":"roots/list"}`)
	if got := r.read(); got["method"] != "roots/list" {
		t.Fatalf("the client got %v", got)
	}
	r.send(`{"jsonrpc":"2.0","id":"w1","result":{"roots":[]}}`)
	if got := w.next(t); got["id"] != "w1" {
		t.Fatalf("the worker got %v", got)
	}
}

// A worker that dies owes the client an answer for every call it held, and
// the client must learn its tools went.
func TestAWorkerThatExitsAnswersWhatItOwedAndTheToolsGo(t *testing.T) {
	r := newRig(t)
	r.handshake()
	r.fail.Store(false)
	w := r.live("a1b2c3d4")
	r.send(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"read_session"}}`)
	w.next(t)
	r.fail.Store(true)
	w.Stop()
	first, second := r.read(), r.read()
	if first["id"] != float64(9) || first["error"] == nil {
		t.Fatalf("the held call was answered %v", first)
	}
	if second["method"] != "notifications/tools/list_changed" {
		t.Fatalf("after the worker went the client got %v", second)
	}
	r.send(`{"jsonrpc":"2.0","id":10,"method":"tools/list"}`)
	if got := r.read(); len(got["result"].(map[string]any)["tools"].([]any)) != 0 {
		t.Fatalf("tools after the worker went = %v", got)
	}
}

func TestLettingThePaneGoStopsTheWorker(t *testing.T) {
	r := newRig(t)
	r.handshake()
	w := r.live("a1b2c3d4")
	r.adopted.Store("")
	if got := r.read(); got["method"] != "notifications/tools/list_changed" {
		t.Fatalf("after the pane was let go the client got %v", got)
	}
	select {
	case _, open := <-w.lines:
		if open {
			t.Fatal("the worker was not stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("the worker was not stopped")
	}
}

// A worker that cannot start is retried with growing gaps, not every tick,
// and the client is never told of tools that never came.
func TestAWorkerThatCannotStartIsNotRetriedEveryTick(t *testing.T) {
	r := newRig(t)
	r.fail.Store(true)
	r.handshake()
	r.adopted.Store("a1b2c3d4")
	time.Sleep(400 * time.Millisecond)
	if tries := len(r.starts); tries == 0 || tries > 6 {
		t.Fatalf("%d starts in 40 ticks, want a backed-off handful", tries)
	}
	r.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if got := r.read(); got["id"] != float64(3) {
		t.Fatalf("the client was sent %v before its answer", got)
	}
}

// A new install is picked up once the worker owes nothing: the session moves
// to a worker on the new binary, and the client re-reads its tools.
func TestAnUpgradeRestartsTheWorkerOnceItIsIdle(t *testing.T) {
	r := newRig(t)
	r.handshake()
	old := r.live("a1b2c3d4")
	r.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"wait_for_session"}}`)
	old.next(t)
	r.upgrade.Store(true)
	time.Sleep(50 * time.Millisecond)
	if len(r.starts) != 0 {
		t.Fatal("the worker was restarted while it held a call")
	}
	r.upgrade.Store(false)
	old.say(`{"jsonrpc":"2.0","id":4,"result":{"content":[]}}`)
	if got := r.read(); got["id"] != float64(4) || got["error"] != nil {
		t.Fatalf("the held call was answered %v", got)
	}
	r.live("a1b2c3d4")
}

// Nothing is announced before the client finishes its handshake.
func TestNoToolsChangeIsSentBeforeTheHandshake(t *testing.T) {
	r := newRig(t)
	r.adopted.Store("a1b2c3d4")
	time.Sleep(50 * time.Millisecond)
	if len(r.starts) != 0 {
		t.Fatal("a worker started before the client initialized")
	}
	r.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if got := r.read(); got["id"] != float64(0) || got["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize answered %v", got)
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

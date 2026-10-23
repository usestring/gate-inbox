package codexinput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Outcome int

const (
	NotSent Outcome = iota
	Accepted
	Unconfirmed
)

// Claim runs after read-only readiness checks, before the only mutating RPC.
// A failed transport after that RPC may have delivered input: callers must
// not retry an Unconfirmed outcome.
type Claim func() (bool, error)

func Deliver(ctx context.Context, threadID, text string, claim Claim) (Outcome, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return NotSent, err
		}
		home = filepath.Join(userHome, ".codex")
	}
	return deliver(ctx, filepath.Join(home, "app-server-control", "app-server-control.sock"), threadID, text, claim)
}

func deliver(ctx context.Context, socket, threadID, text string, claim Claim) (Outcome, error) {
	if threadID == "" {
		return NotSent, errors.New("Codex thread is not bound")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	conn, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Codex control socket redirected")
		}},
	})
	if err != nil {
		return NotSent, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	rpc := client{conn: conn}
	if err := rpc.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "gate_inbox", "version": "1"},
	}, nil); err != nil {
		return NotSent, err
	}
	if err := wsjson.Write(ctx, conn, map[string]string{"method": "initialized"}); err != nil {
		return NotSent, err
	}
	if err := rpc.ready(ctx, threadID); err != nil {
		return NotSent, err
	}
	var turns struct {
		Data []struct{ ID, Status string }
	}
	if err := rpc.call(ctx, "thread/turns/list", map[string]any{
		"threadId": threadID, "sortDirection": "desc", "limit": 1, "itemsView": "notLoaded",
	}, &turns); err != nil {
		return NotSent, err
	}
	if len(turns.Data) != 1 || turns.Data[0].ID == "" || turns.Data[0].Status != "inProgress" {
		return NotSent, errors.New("Codex has no active turn")
	}
	if err := rpc.ready(ctx, threadID); err != nil {
		return NotSent, err
	}
	claimed, err := claim()
	if err != nil || !claimed {
		return NotSent, err
	}
	turnID := turns.Data[0].ID
	var accepted struct{ TurnID string }
	err = rpc.call(ctx, "turn/steer", map[string]any{
		"threadId": threadID, "expectedTurnId": turnID,
		"input": []map[string]string{{"type": "text", "text": text}},
	}, &accepted)
	if err != nil {
		var rejected *rpcError
		if errors.As(err, &rejected) && (rejected.Code == -32600 || rejected.Code == -32601 || rejected.Code == -32602 || rejected.Code == -32001) {
			return NotSent, err
		}
		return Unconfirmed, err
	}
	if accepted.TurnID != turnID {
		return Unconfirmed, errors.New("Codex did not acknowledge the expected turn")
	}
	return Accepted, nil
}

type client struct {
	conn *websocket.Conn
	id   int
}

type rpcError struct {
	Code    int
	Message string
}

func (e *rpcError) Error() string { return fmt.Sprintf("Codex RPC %d: %s", e.Code, e.Message) }

func (c *client) call(ctx context.Context, method string, params, result any) error {
	c.id++
	if err := wsjson.Write(ctx, c.conn, map[string]any{"id": c.id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var response struct {
			ID     json.RawMessage
			Method string
			Result json.RawMessage
			Error  *rpcError
		}
		if err := wsjson.Read(ctx, c.conn, &response); err != nil {
			return err
		}
		if response.Method != "" {
			// Never answer a server request, including approvals and trust.
			if len(response.ID) > 0 {
				return errors.New("Codex requested client input")
			}
			continue
		}
		var id int
		if err := json.Unmarshal(response.ID, &id); err != nil || id != c.id {
			return errors.New("Codex returned an unexpected response id")
		}
		if response.Error != nil {
			return response.Error
		}
		if result != nil {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	}
}

func (c *client) ready(ctx context.Context, threadID string) error {
	var read struct {
		Thread struct {
			ID     string
			Status struct {
				Type        string
				ActiveFlags []string
			}
		}
	}
	if err := c.call(ctx, "thread/read", map[string]string{"threadId": threadID}, &read); err != nil {
		return err
	}
	if read.Thread.ID != threadID || read.Thread.Status.Type != "active" || read.Thread.Status.ActiveFlags == nil || len(read.Thread.Status.ActiveFlags) != 0 {
		return errors.New("Codex is not ready for active-turn input")
	}
	return nil
}

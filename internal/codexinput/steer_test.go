package codexinput

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestSteer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  string
		want  Outcome
		claim bool
	}{
		{"active", "", Accepted, true},
		{"approval", "waitingOnApproval", NotSent, false},
		{"question", "waitingOnUserInput", NotSent, false},
		{"unknown-flag", "newBlockingFlag", NotSent, false},
		{"missing-flags", "missingFlags", NotSent, false},
		{"embedded-thread", "notLoaded", NotSent, false},
		{"completed-turn", "completed", NotSent, false},
		{"dialog-race", "dialogRace", NotSent, false},
		{"approval-request", "serverRequest", NotSent, false},
		{"lost-claim", "lostClaim", NotSent, true},
		{"changed-turn", "rejected", NotSent, true},
		{"internal-error", "internalError", Unconfirmed, true},
		{"lost-ack", "disconnect", Unconfirmed, true},
		{"wrong-turn-ack", "wrongTurn", Unconfirmed, true},
		{"wrong-response-id", "wrongID", Unconfirmed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			socket, received := serveCodex(t, tc.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			claimed := false
			got, err := deliver(ctx, socket, "thread-1", "focus on the regression", func() (bool, error) {
				claimed = true
				return tc.mode != "lostClaim", nil
			})
			if got != tc.want || claimed != tc.claim {
				t.Fatalf("outcome=%v claimed=%v error=%v; want %v/%v", got, claimed, err, tc.want, tc.claim)
			}
			if got == Accepted && err != nil {
				t.Fatal(err)
			}
			var methods []string
			select {
			case methods = <-received:
			case <-ctx.Done():
				t.Fatal("server did not observe disconnect")
			}
			for _, method := range methods {
				if !slices.Contains([]string{"initialize", "initialized", "thread/read", "thread/turns/list", "turn/steer"}, method) {
					t.Fatalf("sent an unsafe method or answered a server request: %q", method)
				}
			}
			steers := 0
			for _, method := range methods {
				if method == "turn/steer" {
					steers++
				}
			}
			wantSteers := 0
			if tc.claim && tc.mode != "lostClaim" {
				wantSteers = 1
			}
			if steers != wantSteers {
				t.Fatalf("steers=%d want %d; methods=%v", steers, wantSteers, methods)
			}
		})
	}
}

func serveCodex(t *testing.T, mode string) (string, <-chan []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "gate-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan []string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		var methods []string
		defer func() { received <- methods }()
		reads := 0
		for {
			var request struct {
				ID     int
				Method string
				Params json.RawMessage
			}
			if err := wsjson.Read(r.Context(), conn, &request); err != nil {
				return
			}
			methods = append(methods, request.Method)
			var result any = map[string]any{}
			switch request.Method {
			case "initialize":
			case "initialized":
				continue
			case "thread/read":
				reads++
				if mode == "serverRequest" {
					wsjson.Write(r.Context(), conn, map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval"})
					continue
				}
				flags := []string{}
				if slices.Contains([]string{"waitingOnApproval", "waitingOnUserInput", "newBlockingFlag"}, mode) {
					flags = append(flags, mode)
				}
				if mode == "dialogRace" && reads == 2 {
					flags = append(flags, "waitingOnApproval")
				}
				status := map[string]any{"type": "active", "activeFlags": flags}
				if mode == "missingFlags" {
					delete(status, "activeFlags")
				}
				if mode == "notLoaded" {
					status["type"] = "notLoaded"
				}
				result = map[string]any{"thread": map[string]any{"id": "thread-1", "status": status}}
			case "thread/turns/list":
				var params struct {
					ThreadID, SortDirection, ItemsView string
					Limit                              int
				}
				if err := json.Unmarshal(request.Params, &params); err != nil {
					t.Error(err)
					return
				}
				if params.ThreadID != "thread-1" || params.SortDirection != "desc" || params.ItemsView != "notLoaded" || params.Limit != 1 {
					t.Errorf("unbounded or wrong thread history request: %+v", params)
				}
				status := "inProgress"
				if mode == "completed" {
					status = "completed"
				}
				result = map[string]any{"data": []map[string]string{{"id": "turn-1", "status": status}}}
			case "turn/steer":
				var params struct {
					ThreadID, ExpectedTurnID string
					Input                    []struct{ Type, Text string }
				}
				if err := json.Unmarshal(request.Params, &params); err != nil {
					t.Error(err)
					return
				}
				if params.ThreadID != "thread-1" || params.ExpectedTurnID != "turn-1" || len(params.Input) != 1 || params.Input[0].Type != "text" || params.Input[0].Text != "focus on the regression" {
					t.Errorf("unsafe steering input: %+v", params)
				}
				if mode == "disconnect" {
					return
				}
				if mode == "rejected" || mode == "internalError" {
					code := -32602
					if mode == "internalError" {
						code = -32603
					}
					wsjson.Write(r.Context(), conn, map[string]any{"id": request.ID, "error": map[string]any{"code": code, "message": "steering failed"}})
					continue
				}
				turn := "turn-1"
				if mode == "wrongTurn" {
					turn = "turn-2"
				}
				if mode == "wrongID" {
					request.ID++
				}
				result = map[string]string{"turnId": turn}
			default:
				t.Errorf("unexpected request: %+v", request)
				return
			}
			if err := wsjson.Write(r.Context(), conn, map[string]any{"id": request.ID, "result": result}); err != nil {
				return
			}
		}
	})}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	return socket, received
}

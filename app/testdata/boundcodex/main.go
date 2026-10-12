package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/usestring/gate-inbox/internal/tmux"
)

type rpc struct {
	ID     any            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func main() {
	if os.Args[1] == "app-server" {
		daemon(os.Args[2], os.Args[3], os.Args[4])
		return
	}
	client(strings.TrimPrefix(os.Args[2], "unix://"), os.Args[3])
}

func client(path, thread string) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	ws, _, err := websocket.Dial(context.Background(), "ws://localhost", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		panic(err)
	}
	defer ws.CloseNow()
	for _, request := range []rpc{{1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "private fixture", "version": "1"}}}, {2, "thread/start", map[string]any{"fixtureThread": thread}}} {
		data, _ := json.Marshal(request)
		if err = ws.Write(context.Background(), websocket.MessageText, data); err != nil {
			panic(err)
		}
		if _, _, err = ws.Read(context.Background()); err != nil {
			panic(err)
		}
	}
	for {
		if _, _, err = ws.Read(context.Background()); err != nil {
			return
		}
	}
}

func daemon(path, root, bin string) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		panic(err)
	}
	var mu sync.Mutex
	active := map[string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			var request rpc
			if err = json.Unmarshal(data, &request); err != nil {
				panic(err)
			}
			var result any = map[string]any{}
			thread, _ := request.Params["threadId"].(string)
			switch request.Method {
			case "thread/start":
				thread = request.Params["fixtureThread"].(string)
				mu.Lock()
				active[thread] = true
				mu.Unlock()
				config := request.Params["config"].(map[string]any)
				set := config["shell_environment_policy"].(map[string]any)["set"].(map[string]any)
				ready, _ := json.Marshal(set)
				os.WriteFile(filepath.Join(root, thread+".ready"), ready, 0600)
				result = map[string]any{"thread": map[string]any{"id": thread}}
				go tool(root, bin, thread, set)
			case "thread/read":
				mu.Lock()
				running := active[thread]
				mu.Unlock()
				state, status := "idle", "completed"
				if running {
					state, status = "active", "inProgress"
				}
				result = map[string]any{"thread": map[string]any{"id": thread, "status": map[string]any{"type": state}, "turns": []any{map[string]any{"id": thread + "-turn", "status": status, "items": []any{map[string]any{"id": thread + "-call", "type": "commandExecution", "status": status}}}}}}
			case "turn/interrupt":
				mu.Lock()
				active[thread] = false
				mu.Unlock()
				f, _ := os.OpenFile(filepath.Join(root, "interrupts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				fmt.Fprintln(f, thread)
				f.Close()
			}
			reply, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
			if err = ws.Write(context.Background(), websocket.MessageText, reply); err != nil {
				return
			}
		}
	})
	if err = http.Serve(listener, handler); err != nil {
		panic(err)
	}
}

func tool(root, bin, thread string, set map[string]any) {
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		path := filepath.Join(root, thread+".go")
		data, err := os.ReadFile(path)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		os.Remove(path)
		var request struct {
			Args []string
			Env  map[string]string
		}
		if err = json.Unmarshal(data, &request); err != nil {
			panic(err)
		}
		words := []string{"env"}
		for key, value := range request.Env {
			words = append(words, key+"="+value)
		}
		words = append(words, bin, "stop")
		words = append(words, request.Args...)
		for i, word := range words {
			words[i] = tmux.ShellQuote(word)
		}
		cmd := exec.Command("/bin/sh", "-c", strings.Join(words, " ")+`; result=$?; exit "$result"`)
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GATE_INBOX_") && !strings.HasPrefix(kv, "CODEX_THREAD_ID=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		for key, value := range set {
			if key != "GATE_INBOX_BIN" {
				cmd.Env = append(cmd.Env, key+"="+value.(string))
			}
		}
		cmd.Env = append(cmd.Env, "CODEX_THREAD_ID="+thread)
		cmd.Env = append(cmd.Env, "CODEX_TOOL_CALL_ID="+thread+"-call")
		out, err := cmd.CombinedOutput()
		result, _ := json.Marshal(map[string]any{"output": string(out), "failed": err != nil})
		os.WriteFile(filepath.Join(root, thread+".result"), result, 0600)
	}
}

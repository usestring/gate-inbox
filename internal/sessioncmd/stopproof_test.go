package sessioncmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const opencodeStopTool = `
[tools.opencode]
command = "echo"
session_store = "opencode"
default_status = "idle"
`

func TestStopProofRequiresTheFreshConversationOutput(t *testing.T) {
	proof := uuid.NewString()
	for _, raw := range []string{"", stopProofPrefix + uuid.NewString(), proof} {
		ctx, cancel := context.WithCancel(context.Background())
		err := awaitStopProof(ctx, proof, func() (string, error) {
			cancel()
			return raw, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unproven caller accepted: %q, %v", raw, err)
		}
	}
	wrapped := stopProofPrefix + proof[:12] + "\n" + proof[12:]
	styled := stopProofPrefix + "\x1b[36m" + proof[:12] + "\x1b[0m\n\x1b[36m" + proof[12:] + "\x1b[0m"
	for _, raw := range []string{wrapped, styled} {
		if err := awaitStopProof(context.Background(), proof, func() (string, error) { return raw, nil }); err != nil {
			t.Fatalf("visible proof rejected: %q, %v", raw, err)
		}
	}
}

func TestStopProofPinsTheConversationAndPaneBeforeActing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		tool         string
		conversation string
	}{
		{"codex", "codex", uuid.NewString()},
		{"opencode", "opencode", "ses_" + strings.ReplaceAll(uuid.NewString(), "-", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSessionHarnessWithConfig(t, sessionConfig+opencodeStopTool)
			row, err := h.store.Get(h.caller.ID)
			if err != nil {
				t.Fatal(err)
			}
			row.Tool = tc.tool
			row.AgentSessionID = tc.conversation
			if err := h.store.UpdateTool(row.ID, row.Tool); err != nil {
				t.Fatal(err)
			}
			if err := h.store.SetAgentSessionID(row.ID, row.AgentSessionID); err != nil {
				t.Fatal(err)
			}
			runtime, err := h.sessions.open()
			if err != nil {
				t.Fatal(err)
			}
			pin, err := runtime.paneProcess(row)
			runtime.store.Close()
			if err != nil {
				t.Fatal(err)
			}
			pane, err := h.driver.PaneID(row.ID)
			if err != nil {
				t.Fatal(err)
			}
			proof := uuid.NewString()
			cmd := exec.Command("tmux", "-L", h.driver.SocketName(), "send-keys", "-t", pane, "-l", stopProofPrefix+proof)
			cmd.Env = tmuxtest.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture output: %v: %s", err, out)
			}
			launch := row.LaunchTime().Format(time.RFC3339Nano)
			for _, wrong := range [][3]string{{"old", pane, pin}, {launch, "%999999", pin}, {launch, pane, "1:old"}} {
				if _, err := h.sessions.FinishStopWithProof(row.ID, wrong[0], wrong[1], wrong[2], row.AgentSessionID, proof, false, true); err == nil {
					t.Fatalf("accepted changed launch or pane: %v", wrong)
				}
			}
			other := uuid.NewString()
			if tc.tool == "opencode" {
				other = "ses_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			}
			if _, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, other, proof, false, true); err == nil {
				t.Fatal("accepted a changed conversation binding")
			}
			result, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, row.AgentSessionID, proof, true, true)
			if err != nil || !result.DryRun || !h.driver.Exists(row.ID) {
				t.Fatalf("proof dry run: %+v, %v", result, err)
			}
			kept, err := h.store.Get(row.ID)
			if err != nil || kept.Archived {
				t.Fatalf("dry run changed lifecycle: %+v, %v", kept, err)
			}
			if _, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, row.AgentSessionID, proof, false, true); err != nil {
				t.Fatal(err)
			}
			ended, err := h.store.Get(row.ID)
			if err != nil || !ended.Archived || h.driver.Exists(row.ID) || !strings.EqualFold(ended.Status, "dead") {
				t.Fatalf("proof did not finish lifecycle: %+v, %v", ended, err)
			}
		})
	}
}

const (
	stopSocket       = "stop-socket"
	stopPane         = "%7"
	stopServerPID    = 4242
	stopConversation = "ses_stopboundfixture"
)

func newOpencodeStopRuntime(t *testing.T) (*sessionHarness, *runtime) {
	t.Helper()
	h := newSessionHarnessWithConfig(t, sessionConfig+opencodeStopTool)
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.store.Close() })
	t.Setenv("TMUX", stopSocket+",4242,0")
	t.Setenv("TMUX_PANE", stopPane)
	return h, runtime
}

func createStopRow(t *testing.T, h *sessionHarness, id, conversation, socket, pane, status string, archived bool) store.Session {
	t.Helper()
	row := store.Session{
		ID: id, Tool: "opencode", Name: id, Status: status, Archived: archived,
		CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: pane, AgentSessionID: conversation,
	}
	if archived {
		row.ArchivedAt = time.Now()
	}
	if err := h.store.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	return row
}

func writeStopMarker(t *testing.T, h *sessionHarness, id, tool, conversation string) {
	t.Helper()
	if err := hooks.NewManager(h.sessions.configDir).SyncAdopted([]hooks.AdoptedPane{{
		ID: id, ServerPID: stopServerPID, PaneID: stopPane, AgentPID: 1, Tool: tool, Conversation: conversation,
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestOpencodeMarkerTargetSelectsTheBoundRowOverAnUnboundDuplicate(t *testing.T) {
	h, runtime := newOpencodeStopRuntime(t)
	createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
	createStopRow(t, h, "dupe0001", stopConversation, "", "", "working", false)
	writeStopMarker(t, h, "bound001", "opencode", stopConversation)
	target, err := runtime.opencodeMarkerTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != "bound001" || target.TmuxPaneID != stopPane {
		t.Fatalf("target = %+v", target)
	}
}

func TestOpencodeMarkerTargetRefusesAmbiguousOrStaleRows(t *testing.T) {
	t.Run("multiple bound rows", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		createStopRow(t, h, "bound002", stopConversation, stopSocket, stopPane, "working", false)
		writeStopMarker(t, h, "bound001", "opencode", stopConversation)
		if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "multiple Gate Inbox rows") {
			t.Fatalf("ambiguous rows: %v", err)
		}
	})
	t.Run("marker row bound elsewhere", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		createStopRow(t, h, "foreig01", stopConversation, stopSocket, "%9", "working", false)
		writeStopMarker(t, h, "foreig01", "opencode", stopConversation)
		if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "bound to another pane") {
			t.Fatalf("foreign marker row: %v", err)
		}
	})
	for _, stopped := range []struct {
		name     string
		status   string
		archived bool
	}{{"dead", "dead", false}, {"archived", "dead", true}} {
		t.Run("marker row "+stopped.name, func(t *testing.T) {
			h, runtime := newOpencodeStopRuntime(t)
			createStopRow(t, h, "stopped1", stopConversation, stopSocket, stopPane, stopped.status, stopped.archived)
			writeStopMarker(t, h, "stopped1", "opencode", stopConversation)
			if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "has stopped") {
				t.Fatalf("stopped marker row: %v", err)
			}
		})
	}
	t.Run("marker names an unbound row", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "unbound1", stopConversation, "", "", "working", false)
		writeStopMarker(t, h, "unbound1", "opencode", stopConversation)
		if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "bound to another pane") {
			t.Fatalf("unbound marker row: %v", err)
		}
	})
	t.Run("marker names no row", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		writeStopMarker(t, h, "missing1", "opencode", stopConversation)
		if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "names no bound Gate Inbox row") {
			t.Fatalf("missing marker row: %v", err)
		}
	})
}

func TestOpencodeMarkerTargetRefusesWithoutAUsableMarker(t *testing.T) {
	t.Run("no marker", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		if _, err := runtime.opencodeMarkerTarget(); !errors.Is(err, ErrUnmanagedCaller) {
			t.Fatalf("no marker: %v", err)
		}
	})
	t.Run("conversation not yet bound", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		writeStopMarker(t, h, "bound001", "opencode", "")
		if _, err := runtime.opencodeMarkerTarget(); err == nil || !strings.Contains(err.Error(), "cannot verify an OpenCode conversation") {
			t.Fatalf("unbound conversation: %v", err)
		}
	})
	t.Run("not an opencode tool", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "bound001", stopConversation, stopSocket, stopPane, "working", false)
		writeStopMarker(t, h, "bound001", "echoer", stopConversation)
		if _, err := runtime.opencodeMarkerTarget(); !errors.Is(err, ErrUnmanagedCaller) {
			t.Fatalf("foreign marker: %v", err)
		}
	})
}

// A session the board launched onto its shared opencode server has no
// adoption marker; the binding its launch wrote stands in for one, but only
// for the row the shell's environment names, for that row's own
// conversation, in that row's own pane.
func TestOpencodeMarkerTargetTakesALaunchedSessionsBinding(t *testing.T) {
	bind := func(t *testing.T, h *sessionHarness, conversation, row string) {
		t.Helper()
		dir := opencode.BindingsDir(h.sessions.configDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, conversation), []byte(row+" tok\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	token := func(t *testing.T, h *sessionHarness, row string) string {
		t.Helper()
		tok, err := parentseal.CallerToken(h.sessions.configDir, row)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	t.Run("bound row in its own pane", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "0c0de001", stopConversation, stopSocket, stopPane, "working", false)
		bind(t, h, stopConversation, "0c0de001")
		t.Setenv(hooks.EnvSessionID, "0c0de001")
		t.Setenv(envname.SessionToken, token(t, h, "0c0de001"))
		target, err := runtime.opencodeMarkerTarget()
		if err != nil {
			t.Fatal(err)
		}
		if target.ID != "0c0de001" {
			t.Fatalf("target = %+v", target)
		}
	})
	// Every session's shells share the server, so naming a row is not
	// enough: another session's shell has its own token, not this row's.
	t.Run("bound row without its token", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "0c0de001", stopConversation, stopSocket, stopPane, "working", false)
		bind(t, h, stopConversation, "0c0de001")
		t.Setenv(hooks.EnvSessionID, "0c0de001")
		t.Setenv(envname.SessionToken, token(t, h, "0c0de002"))
		if _, err := runtime.opencodeMarkerTarget(); !errors.Is(err, ErrUnmanagedCaller) {
			t.Fatalf("another row's token: %v", err)
		}
	})
	t.Run("binding names another row", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "0c0de001", stopConversation, stopSocket, stopPane, "working", false)
		bind(t, h, stopConversation, "other001")
		t.Setenv(hooks.EnvSessionID, "0c0de001")
		if _, err := runtime.opencodeMarkerTarget(); !errors.Is(err, ErrUnmanagedCaller) {
			t.Fatalf("mismatched binding: %v", err)
		}
	})
	t.Run("row in another pane", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "0c0de001", stopConversation, stopSocket, "%9", "working", false)
		bind(t, h, stopConversation, "0c0de001")
		t.Setenv(hooks.EnvSessionID, "0c0de001")
		t.Setenv(envname.SessionToken, token(t, h, "0c0de001"))
		if _, err := runtime.opencodeMarkerTarget(); err == nil {
			t.Fatal("a row bound to another pane was stopped")
		}
	})
	t.Run("no binding", func(t *testing.T) {
		h, runtime := newOpencodeStopRuntime(t)
		createStopRow(t, h, "0c0de001", stopConversation, stopSocket, stopPane, "working", false)
		t.Setenv(hooks.EnvSessionID, "0c0de001")
		if _, err := runtime.opencodeMarkerTarget(); !errors.Is(err, ErrUnmanagedCaller) {
			t.Fatalf("unbound launched row: %v", err)
		}
	})
}

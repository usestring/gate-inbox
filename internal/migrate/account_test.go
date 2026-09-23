package migrate

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountSwitchUsesExactCurrentInputContext(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		input, read, create, output int
		id, tool                    string
		want                        bool
	}{
		{"below", 999, 199000, 0, 9000, "conv", "claude", false},
		{"exact", 1000, 198000, 1000, 9000, "conv", "claude", false},
		{"above", 1001, 198000, 1000, 0, "conv", "claude", true},
		{"uncached", 200001, 0, 0, 0, "conv", "claude", true},
		{"unknown conversation", 200001, 0, 0, 0, "", "claude", false},
		{"different conversation", 200001, 0, 0, 0, "missing", "claude", false},
		{"other CLI", 200001, 0, 0, 0, "conv", "codex", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "projects", "fixture")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fixture","usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d}}}`, time.Now().Format(time.RFC3339Nano), tc.input, tc.read, tc.create, tc.output)
			path := filepath.Join(dir, "conv.jsonl")
			if err := os.WriteFile(path, []byte(raw+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			transcript, got := AccountSwitchTranscript(Roots{ClaudeHome: root}, config.Tool{}, store.Session{ID: "fixture", Tool: tc.tool, AgentSessionID: tc.id, Cwd: "/fixture"})
			if got != tc.want {
				t.Fatalf("migration=%v, want %v", got, tc.want)
			}
			if got && transcript.Path != path {
				t.Fatalf("wrong transcript: %+v", transcript)
			}
		})
	}
}

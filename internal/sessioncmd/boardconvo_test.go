package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const compactedTranscript = `{"type":"user","message":{"role":"user","content":"early work, before the compaction"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"baseline reproduced, moving to the next item"}]}}
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}
{"type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation."}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"drifted off into something nobody asked for"}]}}
`

func TestBoardTranscriptLocatesTheConversation(t *testing.T) {
	h := newSessionHarness(t)
	source, transcript := claudeSource(t, h)
	got, err := h.sessions.BoardTranscript(source.ID)
	if err != nil {
		t.Fatalf("BoardTranscript: %v", err)
	}
	if got.Kind != "claude" || got.Path != transcript {
		t.Fatalf("transcript = %+v, want claude at %s", got, transcript)
	}
	info, err := h.sessions.BoardGet(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.AgentSessionID != source.AgentSessionID {
		t.Fatalf("AgentSessionID = %q, want %q", info.AgentSessionID, source.AgentSessionID)
	}
}

func TestBoardTranscriptRefusesAnUncapturedConversation(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child201", "fresh", answerPane)
	if _, err := h.sessions.BoardTranscript(child.ID); err == nil || !strings.Contains(err.Error(), "no captured conversation id") {
		t.Fatalf("err = %v, want the missing conversation id named", err)
	}
}

func TestBoardHandoverFiltersAndCuts(t *testing.T) {
	h := newSessionHarness(t)
	source, transcript := claudeSource(t, h)
	if err := os.WriteFile(transcript, []byte(compactedTranscript), 0o644); err != nil {
		t.Fatal(err)
	}

	whole, err := h.sessions.BoardHandover(source.ID, BoardHandoverOptions{})
	if err != nil {
		t.Fatalf("BoardHandover: %v", err)
	}
	if !whole.Filtered || whole.Cut || whole.Path != transcript+".handover.jsonl" || !strings.Contains(whole.Note, "compaction") {
		t.Fatalf("whole = %+v, want the compaction cut beside the transcript", whole)
	}
	body, _ := os.ReadFile(whole.Path)
	if strings.Contains(string(body), "early work") {
		t.Fatalf("records before the boundary survived:\n%s", body)
	}

	dest := filepath.Join(t.TempDir(), "rewind.jsonl")
	rewound, err := h.sessions.BoardHandover(source.ID, BoardHandoverOptions{Until: "baseline reproduced, moving to the next item", Dest: dest})
	if err != nil {
		t.Fatalf("BoardHandover until: %v", err)
	}
	if !rewound.Cut || !rewound.Filtered || rewound.Path != dest {
		t.Fatalf("rewound = %+v, want a cut copy at %s", rewound, dest)
	}
	body, _ = os.ReadFile(dest)
	if strings.Contains(string(body), "drifted off") || !strings.Contains(string(body), "baseline reproduced") {
		t.Fatalf("the rewind kept the wrong records:\n%s", body)
	}
	if original, _ := os.ReadFile(transcript); string(original) != compactedTranscript {
		t.Fatal("the transcript itself was modified")
	}
	if _, err := h.sessions.BoardHandover(source.ID, BoardHandoverOptions{Dest: transcript}); err == nil {
		t.Fatal("a copy was allowed to overwrite the transcript it filters")
	}
}

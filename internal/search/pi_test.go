package search

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piFixture is a session pi 1.5.1 wrote on the rig: one bash turn and a
// /name, with the system prompt entry pi records ahead of the first request.
var piFixture = filepath.Join("..", "convo", "testdata", "pi-1.5.1", "named.jsonl")

func piFixtureSegments(t *testing.T) []Segment {
	t.Helper()
	f, err := os.Open(piFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var segs []Segment
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scan.Scan() {
		segs = append(segs, LineSegments(ToolPi, scan.Bytes(), DefaultLimits)...)
	}
	return segs
}

func TestPiSessionIsIndexedByKind(t *testing.T) {
	byKind := map[Kind][]string{}
	for _, seg := range piFixtureSegments(t) {
		byKind[seg.Kind] = append(byKind[seg.Kind], seg.Text)
	}
	if got := byKind[KindUser]; len(got) != 1 || !strings.HasPrefix(got[0], "Use the bash tool to run: echo zebra-quokka-42") {
		t.Errorf("user = %q", got)
	}
	if got := byKind[KindInput]; len(got) != 1 || got[0] != "bash\necho zebra-quokka-42" {
		t.Errorf("tool call = %q", got)
	}
	if got := byKind[KindResult]; len(got) != 1 || strings.TrimSpace(got[0]) != "zebra-quokka-42" {
		t.Errorf("tool result = %q", got)
	}
	if got := byKind[KindAssistant]; len(got) != 1 || got[0] != "the marker is printed" {
		t.Errorf("assistant = %q", got)
	}
	// The system prompt and the model's thinking are not conversation.
	for _, seg := range piFixtureSegments(t) {
		if strings.Contains(seg.Text, "You are a coding assistant") || strings.Contains(seg.Text, "The user wants me") {
			t.Errorf("indexed %v %q", seg.Kind, seg.Text[:min(60, len(seg.Text))])
		}
	}
}

func TestPiExtensionMessagesAreSystemText(t *testing.T) {
	line := []byte(`{"type":"custom_message","id":"i9","parentId":"h8","timestamp":"2026-11-10T11:00:00.000Z","customType":"gate-inbox","content":"verified from your parent","display":false}`)
	segs := LineSegments(ToolPi, line, DefaultLimits)
	if len(segs) != 1 || segs[0].Kind != KindSystem || segs[0].Text != "verified from your parent" {
		t.Errorf("segments = %+v", segs)
	}
}

// A pi row resolves to its session file by id, the term typed into it is
// found, and its opening prompt reads back.
func TestPiRowIsSearchable(t *testing.T) {
	root := t.TempDir()
	const id = "bbbbbbbb-cccc-4444-8888-dddddddddddd"
	path := filepath.Join(root, "--srv-project--", "2026-11-10T11-00-37-733Z_"+id+".jsonl")
	raw, err := os.ReadFile(piFixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLocator("", "")
	l.PiRoot = root
	target, ok := l.Target("row1", ToolPi, "/srv/project", id)
	if !ok || target.Path != path {
		t.Fatalf("Target = %+v %v", target, ok)
	}
	x := openTest(t, Options{})
	refreshAll(t, x, []Target{target})
	if hits := ranked(t, x, "zebra-quokka-42"); len(hits) != 1 || hits[0].Key != "row1" {
		t.Errorf("hits = %+v", hits)
	}
	prompts, err := ReadFirstPrompts(target, 3)
	if err != nil || len(prompts) != 1 || !strings.HasPrefix(prompts[0], "Use the bash tool") {
		t.Errorf("first prompts = %q %v", prompts, err)
	}
}

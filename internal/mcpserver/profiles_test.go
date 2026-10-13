package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/extension/all"
)

const artifactsSection = "[extensions.artifacts]\nenabled = true\nkey_command = \"sh\"\nbase_url = \"https://artifacts.example.test\"\n"

func listProfile(t *testing.T, dir string, profile Profile) ([]*mcp.Tool, string) {
	t.Helper()
	session := connectServer(t, NewProfileServer(dir, "session-1", "test", all.Extensions(), "", profile))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	return listed.Tools, session.InitializeResult().Instructions
}

func sortedNames(tools []*mcp.Tool) []string {
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// The two profiles split the full surface with nothing lost and nothing
// served twice, an extension's tools land on the admin side, and the core
// server is exactly coreTools.
func TestProfilesPartitionTheFullSurface(t *testing.T) {
	dir := configWith(t, artifactsSection)
	full, _ := listProfile(t, dir, ProfileAll)
	core, _ := listProfile(t, dir, ProfileCore)
	admin, _ := listProfile(t, dir, ProfileAdmin)

	coreNames, adminNames := sortedNames(core), sortedNames(admin)
	want := slices.Clone(coreTools)
	sort.Strings(want)
	if !slices.Equal(coreNames, want) {
		t.Fatalf("core serves %v, want %v", coreNames, want)
	}
	for _, name := range adminNames {
		if slices.Contains(coreNames, name) {
			t.Errorf("%s is served by both profiles", name)
		}
	}
	joined := append(slices.Clone(coreNames), adminNames...)
	sort.Strings(joined)
	if !slices.Equal(joined, sortedNames(full)) {
		t.Fatalf("core + admin = %v, all = %v", joined, sortedNames(full))
	}
	if !slices.Contains(adminNames, "publish_artifact") {
		t.Fatalf("an extension's tool did not land in admin: %v", adminNames)
	}
}

// Without --profile the server is what it was before profiles existed.
func TestProfileAllIsTheUnprofiledServer(t *testing.T) {
	dir := configWith(t, "")
	full, instructions := listProfile(t, dir, ProfileAll)
	plain := connectServer(t, NewServer(dir, "session-1", "test", all.Extensions(), ""))
	listed, err := plain.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sortedNames(full), sortedNames(listed.Tools)) {
		t.Fatalf("profile all serves %v, NewServer %v", sortedNames(full), sortedNames(listed.Tools))
	}
	if instructions != plain.InitializeResult().Instructions {
		t.Fatal("profile all changed the server instructions")
	}
}

func TestParseProfile(t *testing.T) {
	for value, want := range map[string]Profile{"": ProfileAll, "all": ProfileAll, "core": ProfileCore, " admin ": ProfileAdmin} {
		got, err := ParseProfile(value)
		if err != nil || got != want {
			t.Errorf("ParseProfile(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParseProfile("charters"); err == nil {
		t.Error("an unknown profile was accepted")
	}
}

// snakeWord is a word the way this surface spells an argument or a tool:
// lower case joined by underscores.
var snakeWord = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)

// passedWord is the word after "pass", which every description uses to
// name an argument of its own: "pass until", "pass children true".
var passedWord = regexp.MustCompile(`\b[Pp]ass ([a-z][a-z0-9_]*)\b`)

// notArgs are the snake_case words descriptions use for something other
// than an argument of the tool they describe: other CLIs' tool names and the
// fields of a result or a session row.
var notArgs = map[string]string{
	"spawn_agent":        "Codex's subagent tool",
	"request_user_input": "Codex's question tool",
	"session_id":         "the header field a cross-session message carries",
}

// Every argument a tool's guidance names is one that tool's schema has, so
// a model following the words does not send a field the schema refuses.
// That is how timeout_seconds reached wait_for_session and subject reached
// send_children in recorded transcripts.
func TestDescriptionsNameOnlyTheirOwnArguments(t *testing.T) {
	tools, _ := listProfile(t, configWith(t, artifactsSection), ProfileAll)
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, tool := range tools {
		props := schemaProperties(t, tool)
		// A word the schemas spell out is one of their values or result
		// fields -- a kind, a state, an outcome -- rather than a stray name.
		schemas, err := json.Marshal([]any{tool.InputSchema, tool.OutputSchema})
		if err != nil {
			t.Fatal(err)
		}
		check := func(word, how string) {
			if props[word] || names[word] || regexp.MustCompile(`\b`+word+`\b`).Match(schemas) {
				return
			}
			if _, ok := notArgs[word]; ok {
				return
			}
			t.Errorf("%s's description names %q (%s), which its schema does not have; schema has %v",
				tool.Name, word, how, sortedKeys(props))
		}
		for _, word := range snakeWord.FindAllString(tool.Description, -1) {
			check(word, "a snake_case word")
		}
		for _, match := range passedWord.FindAllStringSubmatch(tool.Description, -1) {
			if word := match[1]; props[word] || strings.Contains(word, "_") || anyProperty(tools, word) {
				check(word, "after pass")
			}
		}
	}
}

// Each profile's instructions name only tools a session given that profile
// can call: the core server is often the only one, and the admin one always
// sits beside core.
func TestInstructionsNameOnlyServedTools(t *testing.T) {
	dir := configWith(t, "")
	core, coreText := listProfile(t, dir, ProfileCore)
	admin, adminText := listProfile(t, dir, ProfileAdmin)
	full, fullText := listProfile(t, dir, ProfileAll)
	allProps := map[string]bool{}
	for _, tool := range full {
		for prop := range schemaProperties(t, tool) {
			allProps[prop] = true
		}
	}
	for _, c := range []struct {
		profile Profile
		text    string
		served  []*mcp.Tool
	}{
		{ProfileCore, coreText, core},
		{ProfileAdmin, adminText, append(slices.Clone(core), admin...)},
		{ProfileAll, fullText, full},
	} {
		served := map[string]bool{}
		for _, tool := range c.served {
			served[tool.Name] = true
		}
		for _, word := range snakeWord.FindAllString(c.text, -1) {
			if served[word] || allProps[word] {
				continue
			}
			if _, ok := notArgs[word]; ok {
				continue
			}
			t.Errorf("%s instructions name %q, which no tool it is served with has", c.profile, word)
		}
	}
}

// The names models were recorded sending in place of the schema's are
// served as what they meant, and the schema's own name wins when both come.
func TestDriftedArgumentNamesAreServed(t *testing.T) {
	fake := &fakeSessionCommands{}
	session := connectServer(t, newServer(t.TempDir(), "session-1", "test", &fakeTerminalCommands{}, fake))
	ctx := context.Background()

	call := func(args map[string]any) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wait_for_session", Arguments: args})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if result.IsError {
			t.Fatalf("refused: %v", result.Content)
		}
	}
	call(map[string]any{"session_id": "a1b2c3d4", "timeout_seconds": 7})
	if fake.waitedTimeout != 7*time.Second {
		t.Fatalf("timeout_seconds 7 waited %v", fake.waitedTimeout)
	}
	call(map[string]any{"session_id": "a1b2c3d4", "timeout_seconds": 7, "timeout_s": 9})
	if fake.waitedTimeout != 9*time.Second {
		t.Fatalf("timeout_s 9 beside timeout_seconds waited %v", fake.waitedTimeout)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "send_children",
		Arguments: map[string]any{"message": "the branch moved", "subject": "branch"}})
	if err != nil || result.IsError {
		t.Fatalf("send_children with subject: %v %v", err, result)
	}
	if fake.sentSubject != "branch" {
		t.Fatalf("send_children subject = %q", fake.sentSubject)
	}

	terminals := &fakeTerminalCommands{}
	session = connectServer(t, newServer(t.TempDir(), "session-1", "test", terminals, fake))
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_terminal",
		Arguments: map[string]any{"name": "deploy-shell"}})
	if err != nil || result.IsError {
		t.Fatalf("create_terminal with name: %v %v", err, result)
	}
	if terminals.createdOpts.Name != "deploy-shell" {
		t.Fatalf("create_terminal name = %q", terminals.createdOpts.Name)
	}
}

func schemaProperties(t *testing.T, tool *mcp.Tool) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props := map[string]bool{}
	for name := range schema.Properties {
		props[name] = true
	}
	return props
}

func anyProperty(tools []*mcp.Tool, word string) bool {
	for _, tool := range tools {
		raw, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(raw), `"`+word+`":`) {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	var keys []string
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

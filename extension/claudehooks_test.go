package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// hooker runs on Claude Code hooks, built from the public package alone.
type hooker struct {
	id     string
	hooks  []extension.ClaudeHook
	off    bool
	refuse error
	out    string
	err    error
	panics bool
	got    *extension.ClaudeHookCall
}

func (h *hooker) Descriptor() extension.Descriptor    { return extension.Descriptor{ID: h.id} }
func (h *hooker) Configure(extension.Config) error    { return h.refuse }
func (h *hooker) Enabled() bool                       { return !h.off }
func (h *hooker) ClaudeHooks() []extension.ClaudeHook { return h.hooks }

func (h *hooker) RunClaudeHook(_ context.Context, call extension.ClaudeHookCall) ([]byte, error) {
	if h.panics {
		panic("boom")
	}
	h.got = &call
	return []byte(h.out), h.err
}

var slackHook = extension.ClaudeHook{Event: "PreToolUse", Matcher: "mcp__slack__conversations_add_message"}

func TestClaudeHooksListsProvidersInOrder(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{
		&hooker{id: "b", hooks: []extension.ClaudeHook{slackHook, {Event: "Stop"}}},
		&stub{id: "plain"},
		&hooker{id: "a", hooks: []extension.ClaudeHook{{Event: "PostToolUse", Matcher: "Edit|Write"}}, off: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := registry.ClaudeHooks()
	if err != nil {
		t.Fatal(err)
	}
	want := []extension.RegisteredClaudeHook{
		{ID: "b", ClaudeHook: slackHook},
		{ID: "b", ClaudeHook: extension.ClaudeHook{Event: "Stop"}},
		// Listed whatever its config says: the settings file is the build's.
		{ID: "a", ClaudeHook: extension.ClaudeHook{Event: "PostToolUse", Matcher: "Edit|Write"}},
	}
	if len(got) != len(want) {
		t.Fatalf("hooks = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hook %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestClaudeHooksRefusesBadHooks(t *testing.T) {
	for _, hook := range []extension.ClaudeHook{
		{Event: "SessionEnd"},
		{Event: "pretooluse"},
		{Event: "PreToolUse", Matcher: "mcp__slack__.*"},
		{Event: "PreToolUse", Matcher: "Bash; rm -rf /"},
		{Event: "PreToolUse", Matcher: "Bash|"},
		{Event: "PreToolUse", Matcher: "Notebook.dit"},
	} {
		registry, err := extension.NewRegistry([]extension.Extension{&hooker{id: "bad", hooks: []extension.ClaudeHook{hook}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ClaudeHooks(); err == nil || !strings.Contains(err.Error(), `"bad"`) {
			t.Fatalf("%+v: err = %v, want a refusal naming the extension", hook, err)
		}
	}
}

func TestRunClaudeHookHandsOverTheCall(t *testing.T) {
	ext := &hooker{id: "policy", hooks: []extension.ClaudeHook{slackHook}, out: `{"hookSpecificOutput":{"hookEventName":"PreToolUse"}}`}
	registry, err := extension.NewRegistry([]extension.Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	call := extension.ClaudeHookCall{Event: "PreToolUse", SessionID: "s1", Payload: []byte(`{"tool_name":"x"}`)}
	out, err := registry.RunClaudeHook(context.Background(), "", nil, "policy", call)
	if err != nil || string(out) != ext.out {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if ext.got == nil || ext.got.SessionID != "s1" || string(ext.got.Payload) != `{"tool_name":"x"}` {
		t.Fatalf("extension got %+v", ext.got)
	}
}

func TestRunClaudeHookAddsNothingWhenItCannotRun(t *testing.T) {
	call := extension.ClaudeHookCall{Event: "PreToolUse", Payload: []byte(`{}`)}
	for _, tc := range []struct {
		name    string
		ext     *hooker
		id      string
		event   string
		wantErr bool
	}{
		{name: "switched off", ext: &hooker{off: true, out: `{}`}},
		{name: "refused", ext: &hooker{refuse: errors.New("bad section"), out: `{}`}, wantErr: true},
		{name: "unknown id", ext: &hooker{out: `{}`}, id: "other", wantErr: true},
		{name: "undeclared event", ext: &hooker{out: `{}`}, event: "Stop", wantErr: true},
		{name: "fails", ext: &hooker{out: `{}`, err: errors.New("no")}, wantErr: true},
		{name: "panics", ext: &hooker{panics: true}, wantErr: true},
		{name: "not an object", ext: &hooker{out: `["x"]`}, wantErr: true},
		{name: "not JSON", ext: &hooker{out: `signed`}, wantErr: true},
		{name: "empty", ext: &hooker{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ext.id = "policy"
			tc.ext.hooks = []extension.ClaudeHook{slackHook}
			registry, err := extension.NewRegistry([]extension.Extension{tc.ext})
			if err != nil {
				t.Fatal(err)
			}
			id, c := "policy", call
			if tc.id != "" {
				id = tc.id
			}
			if tc.event != "" {
				c.Event = tc.event
			}
			out, err := registry.RunClaudeHook(context.Background(), "", nil, id, c)
			if len(out) != 0 || (err != nil) != tc.wantErr {
				t.Fatalf("out = %q, err = %v, want no output and error=%v", out, err, tc.wantErr)
			}
		})
	}
}

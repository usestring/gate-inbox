package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// ClaudeHookProvider is implemented by an extension that runs on Claude
// Code's own hook events in the sessions the board manages: a policy on what
// a tool call may carry, a note on a prompt, a check when a turn stops. The
// board's hooks are how it reads a session; this is how an extension adds
// its own beside them, without the core knowing what any of them is for.
//
// ClaudeHooks names the events and matchers the extension runs on. It is
// asked with no config, in every process that writes the hook settings a
// session launches with, so it must return the same list every time and
// must not depend on Configure: the settings file is shared by every
// session on the board and named by its content, and two processes that
// disagreed on it would rewrite it under each other. A hook whose extension
// its config switches off still fires; RunClaudeHook is never asked, and the
// hook adds nothing.
//
// Each hook costs every session a process start whenever it fires, so a
// tool event names the tools it is about: a matcher of "*" on PreToolUse
// starts a process for every tool call a session makes.
//
// RunClaudeHook is called, in a process of its own, each time one of those
// hooks fires in a managed session. Call carries the event and Claude Code's
// payload exactly as the hook read it on stdin. What it returns is the
// hook's output, which Claude Code reads as one JSON object -- for
// PreToolUse that is where hookSpecificOutput.updatedInput and
// permissionDecision go -- or nothing, which leaves the event as it was. An
// output that is not a JSON object is dropped, and so is everything on an
// error or a panic: a hook that fails must not stand in the session's way,
// so a policy that has to hold fails closed through its output, not through
// an error.
//
// It runs on the session's critical path -- a PreToolUse hook holds the tool
// call until it returns -- so it is held to Configure's rule: local work
// only, no network, no credentials.
type ClaudeHookProvider interface {
	ClaudeHooks() []ClaudeHook
	RunClaudeHook(ctx context.Context, call ClaudeHookCall) ([]byte, error)
}

// ClaudeHook is one Claude Code hook an extension runs on.
type ClaudeHook struct {
	// Event is one of ClaudeHookEvents.
	Event string
	// Matcher is what Claude Code compares the event's matched field with:
	// tool names for PreToolUse and PostToolUse, the notification type for
	// Notification, the source for SessionStart, separated by |. Empty
	// matches everything, as does "*".
	Matcher string
}

// ClaudeHookCall is one firing of a ClaudeHook.
type ClaudeHookCall struct {
	Event string
	// SessionID is the board row the session is, or empty for a session
	// the board does not know.
	SessionID string
	// Payload is the hook's input as Claude Code wrote it: for PreToolUse,
	// tool_name and tool_input among the rest.
	Payload []byte
}

// ClaudeHookEvents are the events an extension can run on: the ones a
// session the board adopted rather than launched carries too, so an
// extension's hooks reach every session on the board alike.
var ClaudeHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure"}

// matcherPattern is the matcher a hook may carry: names separated by |, the
// only form the global dispatch for adopted sessions reads (matcherTakes in
// internal/hooks). It keeps to the characters a tool name is made of and
// leaves out regular-expression syntax, so Claude Code cannot match a
// launched session's hook more widely than an adopted one's.
var matcherPattern = regexp.MustCompile(`^(\*|[A-Za-z0-9_-]+(\|[A-Za-z0-9_-]+)*)?$`)

// RegisteredClaudeHook is one extension's ClaudeHook, with the ID of the
// extension that runs it.
type RegisteredClaudeHook struct {
	ID string
	ClaudeHook
}

// ClaudeHooks lists every ClaudeHookProvider's hooks, in registration order.
// It configures nothing and asks no Enabler: see ClaudeHookProvider. A hook
// on an event an extension cannot run on, or with a malformed matcher, is
// refused, as is a provider that panics while listing them.
func (r *Registry) ClaudeHooks() ([]RegisteredClaudeHook, error) {
	var out []RegisteredClaudeHook
	var errs []error
	for i, ext := range r.extensions {
		provider, ok := ext.(ClaudeHookProvider)
		if !ok {
			continue
		}
		id := r.ids[i]
		hooks, err := listClaudeHooks(provider)
		if err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", id, err))
			continue
		}
		for _, hook := range hooks {
			switch {
			case !slices.Contains(ClaudeHookEvents, hook.Event):
				errs = append(errs, fmt.Errorf("extension %q: Claude hook event %q is not one an extension can run on (%v)", id, hook.Event, ClaudeHookEvents))
			case !matcherPattern.MatchString(hook.Matcher):
				errs = append(errs, fmt.Errorf("extension %q: Claude hook matcher %q must be names separated by |, or *", id, hook.Matcher))
			default:
				out = append(out, RegisteredClaudeHook{ID: id, ClaudeHook: hook})
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func listClaudeHooks(provider ClaudeHookProvider) (hooks []ClaudeHook, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			hooks, err = nil, fmt.Errorf("panicked while listing Claude hooks: %v", recovered)
		}
	}()
	return provider.ClaudeHooks(), nil
}

// RunClaudeHook hands call to the extension with id, configuring only that
// one, from sections and under configDir, when nothing has configured the
// registry yet: the hook process has no use for the rest. It returns the
// hook output to print, or nothing when the extension is not this build's,
// is switched off or refused by its config, has no hook on call's event, or
// fails. An output that is not one JSON object is dropped, since Claude Code
// would refuse it whole.
func (r *Registry) RunClaudeHook(ctx context.Context, configDir string, sections map[string]map[string]any, id string, call ClaudeHookCall) ([]byte, error) {
	i := slices.Index(r.ids, id)
	if i < 0 {
		return nil, fmt.Errorf("no extension %q in this build", id)
	}
	provider, ok := r.extensions[i].(ClaudeHookProvider)
	if !ok {
		return nil, fmt.Errorf("extension %q runs on no Claude hook", id)
	}
	declared, err := listClaudeHooks(provider)
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", id, err)
	}
	if !slices.ContainsFunc(declared, func(hook ClaudeHook) bool { return hook.Event == call.Event }) {
		return nil, fmt.Errorf("extension %q runs on no %s hook", id, call.Event)
	}
	if err := r.configureOnly([]int{i}, configDir, sections)[id]; err != nil {
		return nil, disabledError(id, err)
	}
	if !enabled(r.extensions[i]) {
		return nil, nil
	}
	out, err := runClaudeHook(ctx, provider, call)
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", id, err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal(out, &object); err != nil || object == nil {
		return nil, fmt.Errorf("extension %q: %s hook output is not a JSON object", id, call.Event)
	}
	return out, nil
}

func runClaudeHook(ctx context.Context, provider ClaudeHookProvider, call ClaudeHookCall) (out []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out, err = nil, fmt.Errorf("panicked while running a %s hook: %v", call.Event, recovered)
		}
	}()
	return provider.RunClaudeHook(ctx, call)
}

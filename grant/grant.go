// Package grant is the one place a permission is widened for a session Gate
// Inbox runs -- what a grant may be, and how it is written into a Claude Code settings file.
//
// A parent session grants its own child a permission its user approved
// (sessioncmd's grant_permission), and an extension that supervises sessions
// can grant them one too. Both write the same kinds of rule under the same
// refusals, so the refusals live here rather than in either of them.
//
// What Go keeps for itself, because it is not a judgement: a grant has to be
// specific enough to be a decision at all -- "Bash" or "Bash(*)" grants
// everything the tool can do, which is not a narrower answer to anything -- and
// no grant may reach the permission system itself or a credential. A session
// that can widen its own authority has no boundary, and the whole arrangement
// rests on that boundary holding. There is deliberately no way to grant past a
// refusal; a person who wants one edits their own settings.
package grant

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Kind is what a grant widens.
type Kind string

const (
	// KindRule is one permission rule, Tool(pattern), added to
	// permissions.allow.
	KindRule Kind = "rule"
	// KindUnsandboxed is one exact command prefix that runs outside the
	// sandbox without asking: the prefix goes to sandbox.excludedCommands and
	// Bash(prefix:*) to permissions.allow. The exclusion alone still leaves
	// the call to the permission flow, and the allow rule alone leaves it in
	// the sandbox, so neither half settles the case without the other.
	KindUnsandboxed Kind = "unsandboxed_command"
	// KindDomain is one network host the sandbox's proxy lets through, added
	// to sandbox.network.allowedDomains.
	KindDomain Kind = "domain"
	// KindSoft is one exact command prefix the auto-mode classifier is told
	// the user approved, including outside the sandbox: a prose entry in
	// autoMode.allow. It adds no permission rule, so the classifier still
	// judges every call and can refuse one that does more than the prefix
	// names. The classifier reads autoMode from user, managed and --settings
	// sources only, never from a project's files, which is why a session's
	// own settings file can carry it.
	KindSoft Kind = "soft_command"
)

// Kinds is every kind, in the order they are offered.
var Kinds = []Kind{KindSoft, KindRule, KindUnsandboxed, KindDomain}

// Grant is one permission, as asked for and as recorded.
type Grant struct {
	Kind  Kind
	Value string
}

// MinPrefix is how much literal text a rule must carry before its first
// wildcard. Four characters is not a security boundary and is not meant as
// one: it is the line between a rule that names something ("Bash(bash ./scripts/
// *.sh)") and one that names a tool ("Bash(b*)"), which is the difference
// between a decision and a blank cheque.
const MinPrefix = 4

// reach are the things no grant may touch, matched anywhere in it: the
// permission system and the credentials, and nothing else.
var reach = []string{
	"settings.json",
	"settings.local.json",
	"managed-settings",
	"claude-settings",
	".claude",
	"sudo",
	".ssh",
	".aws",
	"credential",
	"doppler",
	"id_rsa",
	".netrc",
	".env",
}

// defaultsEntry is how an autoMode list keeps Claude Code's built-in entries.
const defaultsEntry = "$defaults"

// commandMeta are the characters that would let a command prefix be more than
// one command. A prefix is matched against the start of what the agent runs,
// so "tools/x.sh;" would be a prefix of anything at all once it ran.
const commandMeta = ";&|`$<>\n\r"

// hostPattern is a host name, optionally behind one leading "*." -- the only
// wildcard the sandbox's domain list reads.
var hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Error is a grant Gate Inbox will not write, and why. Its message is read by
// the person or agent that proposed it, so it says what was proposed and what
// is wrong with it.
type Error struct {
	Grant  Grant
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("the %s %q cannot be granted: %s", e.Grant.Kind, e.Grant.Value, e.Reason)
}

// Normalise is g with its value trimmed and, for a domain, lower-cased.
func Normalise(g Grant) Grant {
	g.Kind = Kind(strings.TrimSpace(string(g.Kind)))
	g.Value = strings.TrimSpace(g.Value)
	if g.Kind == KindDomain {
		g.Value = strings.ToLower(g.Value)
	}
	return g
}

// Check reports whether g is one Gate Inbox will write, returning an *Error
// for one it will not.
func Check(g Grant) error {
	g = Normalise(g)
	switch g.Kind {
	case KindRule:
		return checkRule(g, g.Value)
	case KindUnsandboxed, KindSoft:
		if strings.ContainsAny(g.Value, commandMeta) {
			return &Error{Grant: g, Reason: "it carries a shell separator, redirect or substitution, so it is " +
				"more than one command; name the command itself"}
		}
		if strings.ContainsAny(g.Value, "*?") {
			return &Error{Grant: g, Reason: "a command prefix is matched literally; leave the wildcard out"}
		}
		return checkRule(g, "Bash("+g.Value+":*)")
	case KindDomain:
		if err := reached(g, g.Value); err != nil {
			return err
		}
		if !hostPattern.MatchString(g.Value) {
			return &Error{Grant: g, Reason: "it is not a host name such as api.example.com " +
				"(one leading \"*.\" is the only wildcard)"}
		}
		if strings.HasPrefix(g.Value, "*.") && strings.Count(g.Value, ".") < 2 {
			return &Error{Grant: g, Reason: "it is a wildcard over a whole top-level domain"}
		}
		return nil
	}
	return &Error{Grant: g, Reason: fmt.Sprintf("there is no such kind; use one of %s", kindList())}
}

// CheckRule reports whether a permission rule is one Gate Inbox will write.
func CheckRule(rule string) error {
	return Check(Grant{Kind: KindRule, Value: rule})
}

func checkRule(g Grant, rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return &Error{Grant: g, Reason: "it is empty"}
	}
	if err := reached(g, rule); err != nil {
		return err
	}
	open := strings.Index(rule, "(")
	if open <= 0 || !strings.HasSuffix(rule, ")") {
		return &Error{Grant: g, Reason: "it names a whole tool rather than what to allow; " +
			"write it as Tool(pattern), which is the syntax the harness reads"}
	}
	pattern := strings.TrimSpace(rule[open+1 : len(rule)-1])
	if pattern == "" {
		return &Error{Grant: g, Reason: "it allows the tool with no pattern at all"}
	}
	literal := pattern
	if star := strings.IndexAny(pattern, "*?"); star >= 0 {
		literal = pattern[:star]
	}
	literal = strings.TrimSuffix(strings.TrimSpace(literal), ":")
	if len(strings.TrimSpace(literal)) < MinPrefix {
		return &Error{Grant: g, Reason: fmt.Sprintf(
			"it is a wildcard with nothing in front of it (%q), which allows anything the tool "+
				"can do rather than the step that was refused", pattern)}
	}
	return nil
}

func reached(g Grant, text string) error {
	lower := strings.ToLower(text)
	for _, r := range reach {
		if strings.Contains(lower, r) {
			return &Error{Grant: g, Reason: fmt.Sprintf(
				"it names %q, and the permission system and the credentials are outside "+
					"what any session may widen for another", r)}
		}
	}
	return nil
}

func kindList() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

// Rules is the permissions.allow entries g writes.
func Rules(g Grant) []string {
	g = Normalise(g)
	switch g.Kind {
	case KindRule:
		return []string{g.Value}
	case KindUnsandboxed:
		return []string{"Bash(" + g.Value + ":*)"}
	}
	return nil
}

// Describe is g in a sentence fragment, for the approval question and the
// board: what the session will be able to do.
func Describe(g Grant) string {
	g = Normalise(g)
	switch g.Kind {
	case KindRule:
		return fmt.Sprintf("the permission rule %s", g.Value)
	case KindUnsandboxed:
		return fmt.Sprintf("running commands that start with %q outside the sandbox without asking "+
			"(sandbox exclusion plus the rule %s)", g.Value, Rules(g)[0])
	case KindDomain:
		return fmt.Sprintf("network access to %s from inside the sandbox", g.Value)
	case KindSoft:
		return fmt.Sprintf("a note to its auto-mode classifier that you approved running commands that start "+
			"with %q, including outside the sandbox (the classifier still judges each call)", g.Value)
	}
	return fmt.Sprintf("%s %q", g.Kind, g.Value)
}

// SoftRule is the autoMode.allow entry a soft grant writes. Gate Inbox words
// it, never the parent: the entry is read by a model, and a parent's own
// prose would be one more way to talk it past what the user approved.
func SoftRule(prefix string) string {
	return fmt.Sprintf("Gate Inbox grant: the user approved running commands that start with %q in this "+
		"session, including with dangerouslyDisableSandbox: true. Running such a command, alone, is not Safety "+
		"Bypass Flag, Security Weaken or Auto-Mode Bypass. It covers nothing else: any other command, or "+
		"anything chained to this one, is judged by every other rule.", prefix)
}

// Apply merges grants into a Claude Code settings document, in place. The
// document is somebody else's -- hooks, a status line, the operator's own
// rules -- so it is added to, never replaced, and a block of the wrong shape
// is left as it is rather than overwritten.
func Apply(settings map[string]any, grants []Grant) {
	for _, g := range grants {
		g = Normalise(g)
		for _, rule := range Rules(g) {
			appendAt(settings, rule, "permissions", "allow")
		}
		switch g.Kind {
		case KindUnsandboxed:
			appendAt(settings, g.Value, "sandbox", "excludedCommands")
		case KindDomain:
			appendAt(settings, g.Value, "sandbox", "network", "allowedDomains")
		case KindSoft:
			// The list replaces the built-in exceptions unless it names them,
			// and a grant must not take any away.
			appendAt(settings, defaultsEntry, "autoMode", "allow")
			appendAt(settings, SoftRule(g.Value), "autoMode", "allow")
		}
	}
}

// appendAt adds value to the string array at path, making the objects on the
// way. It adds nothing already there, and touches nothing of the wrong type.
func appendAt(settings map[string]any, value string, path ...string) {
	block := settings
	for _, key := range path[:len(path)-1] {
		next, ok := block[key].(map[string]any)
		if !ok {
			if _, present := block[key]; present {
				return
			}
			next = map[string]any{}
			block[key] = next
		}
		block = next
	}
	last := path[len(path)-1]
	list, ok := block[last].([]any)
	if !ok {
		if _, present := block[last]; present {
			return
		}
	}
	if slices.ContainsFunc(list, func(have any) bool { return have == value }) {
		return
	}
	block[last] = append(list, value)
}

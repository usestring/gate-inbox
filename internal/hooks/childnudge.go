package hooks

import "os"

// ChildDialogsCommand runs the child-dialogs hook verb (sessioncmd.ChildNudgeHook)
// on the hook's stdin, in a managed session only.
func ChildDialogsCommand() string {
	return hookCommandLine("child-dialogs")
}

// ChildSegmentStampSuffix names the file beside a session's status log whose
// time is the last refresh ThrottledChildDialogsCommand let through.
const ChildSegmentStampSuffix = ".child-seg"

// ThrottledChildDialogsCommand is ChildDialogsCommand at most once a minute,
// for an event as frequent as PostToolUse: the shell checks the stamp's age
// before starting the binary, so the calls it skips cost a find and nothing
// more. A minute keeps the status line's segment well inside the ten minutes
// after which the status line drops it.
func ThrottledChildDialogsCommand() string {
	stamp := `"$f` + ChildSegmentStampSuffix + `"`
	return statusFileVar + `[ -z "$f" ] || [ -n "$(find ` + stamp + ` -mmin -1 2>/dev/null)" ] || { touch ` + stamp +
		` 2>/dev/null; [ -z "$` + EnvExecutable + `" ] || [ -z "$` + EnvSessionID + `" ] || "$` + EnvExecutable +
		`" hook child-dialogs 2>/dev/null; }; exit 0`
}

// ParentRuleSuffix names the marker beside a session's status log saying its
// SessionStart hook has shown it the parent rule (sessioncmd.ChildApprovalNote).
// A Claude Code session hears the rule from that hook alone, and only when it
// had a spawner as it started or compacted: a session placed under a parent
// later was never told it. The short parent notes are only for sessions this
// marker vouches for.
const ParentRuleSuffix = ".parent-rule"

// MarkParentRuleShown records that the session was just shown the parent rule.
func (m *Manager) MarkParentRuleShown(id string) error {
	return WriteWhole(m.StatusFile(id)+ParentRuleSuffix, "")
}

// ParentRuleShown reports whether the session has been shown the parent rule
// since it last started or compacted.
func (m *Manager) ParentRuleShown(id string) bool {
	_, err := os.Stat(m.StatusFile(id) + ParentRuleSuffix)
	return err == nil
}

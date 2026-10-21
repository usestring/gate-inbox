package hooks

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

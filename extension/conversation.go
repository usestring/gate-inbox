package extension

// Transcript is where one session's conversation is kept by its agent CLI.
type Transcript struct {
	// Kind is the layout: "claude" and "codex" are files of one JSON
	// record per line, "opencode" is a JSON document Command prints.
	Kind string
	// Path is the transcript file; empty when Command is set instead.
	Path string
	// Command is a shell command that prints the transcript when run from
	// the session's directory, for a tool that keeps it in a database.
	Command string
}

// HandoverOptions shapes one Board.Handover. The zero value is the copy a
// migration hands over.
type HandoverOptions struct {
	// Until ends the copy at the last record whose conversation text holds
	// it, and drops everything after: a rewind to the last turn that was
	// still on course. A quote that matches nothing leaves Handover.Cut
	// false and filters the whole conversation.
	Until string
	// Dest is the file the copy is written to; empty writes it beside the
	// transcript.
	Dest string
}

// Handover is one filtered transcript.
//
// The filter drops everything before the last compaction (the summary
// there carries it), collapses records repeated in a loop, and stubs turns
// that declined the task. The transcript itself is never modified.
type Handover struct {
	// Path is the file to hand over: the filtered copy, or the transcript
	// itself when nothing was removed.
	Path string
	// Note says what the filter removed, addressed to the agent that reads
	// Path; empty when it removed nothing.
	Note string
	// Cut says HandoverOptions.Until matched and the copy ends there.
	Cut bool
	// Filtered says Path is a copy with something removed, rather than the
	// transcript itself.
	Filtered bool
}

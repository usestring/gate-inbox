package mcpreg

// ChildReportNote tells a spawned session how to end its work. A parent
// learns what its child did from the child's last message (read_session's
// digest carries it), and that parent saw none of the work, so the message
// has to stand alone and lead with what the parent acts on.
//
// It is one sentence in one place: a claude or pi child gets it from its
// SessionStart hook beside the approval note (sessioncmd.SessionStartHook),
// and a CLI with no such hook from its launch steering. ASCII for the reason
// DelegationSteering is.
const ChildReportNote = "When you finish or stop the work your parent gave you, your last message is what " +
	"it reads, and it saw none of the work: give the outcome first, then what you verified and how, " +
	"what you did not do, the decisions you made alone, and where the work is (PR, branch or file)."

const childReportHeading = "# Reporting back to the session that spawned you"

// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"fmt"
	"strings"
)

// The CLI subcommands and the MCP tools describe the same workspace to the
// same agents, so the sentences live beside the types both fronts return.

func groupLabel(group string) string {
	if group == "" {
		return "root"
	}
	return group
}

func FormatTerminal(terminal Terminal) string {
	return fmt.Sprintf("%s (id %s) in %s at %s", terminal.Name, terminal.ID, groupLabel(terminal.Group), terminal.Directory)
}

func FormatTerminalList(terminals []Terminal) string {
	if len(terminals) == 0 {
		return "no managed terminals"
	}
	lines := make([]string, 0, len(terminals))
	for _, terminal := range terminals {
		lines = append(lines, fmt.Sprintf("- %s; status=%s; running=%t", FormatTerminal(terminal), terminal.Status, terminal.Running))
	}
	return strings.Join(lines, "\n")
}

func FormatTerminalInput(input TerminalInput) string {
	return fmt.Sprintf("sent %s to terminal %s", input.Sent, input.TerminalID)
}

func FormatTerminalScreen(screen TerminalScreen) string {
	if screen.Output == "" {
		return "terminal screen is empty"
	}
	return screen.Output
}

func FormatSession(session Session) string {
	tool := session.Tool
	// The model rides with the CLI it qualifies, and is left off entirely
	// when the session is on that CLI's own default -- which is most of them.
	if session.Model != "" {
		tool += " (" + session.Model + ")"
	}
	// The account likewise: a session on the CLI's own login says nothing.
	if session.Account != "" {
		tool += " as " + session.Account
	}
	line := fmt.Sprintf("%s (id %s) running %s in %s at %s", session.Name, session.ID, tool, groupLabel(session.Group), session.Directory)
	// Named on the row rather than drawn as a tree: these lines are read one
	// at a time, and a caller checking where its own spawn landed should not
	// have to infer that from the order of a list.
	if session.ParentID != "" {
		line += " under " + session.ParentID
	}
	return line
}

func FormatSessionList(list SessionList) string {
	if len(list.Sessions) == 0 {
		return "no agent sessions matched"
	}
	lines := make([]string, 0, len(list.Sessions)+1)
	for _, session := range list.Sessions {
		line := fmt.Sprintf("- %s; status=%s; running=%t", FormatSession(session), session.Status, session.Running)
		if session.Archived {
			line += "; archived"
		}
		if session.Self {
			line += "; this session"
		}
		lines = append(lines, line)
	}
	if list.Truncated {
		lines = append(lines, fmt.Sprintf("(%d of %d matching sessions; narrow parent or status, or raise limit)", list.Returned, list.Matched))
	}
	return strings.Join(lines, "\n")
}

// FormatArchiveState reads the verb off the row the call returned, so
// neither front says "archived" about a row that came back active.
func FormatArchiveState(session Session) string {
	if session.Archived {
		return "archived " + FormatSession(session)
	}
	return "restored " + FormatSession(session)
}

// FormatSessionScreen leads with the digest, because the questions a reader
// has -- has it finished, is it stuck on something -- are answered there, and
// a reader that has them answered can stop reading. The output follows, and
// is the pane or the delta depending on what was asked for.
func FormatSessionScreen(screen SessionScreen) string {
	var parts []string
	if screen.Digest.Status != "" {
		parts = append(parts, "status: "+screen.Digest.Status)
	}
	if screen.Digest.Question != "" {
		parts = append(parts, "waiting on:\n"+screen.Digest.Question)
	}
	if screen.Digest.Result != "" {
		parts = append(parts, "last said:\n"+screen.Digest.Result)
	}
	if screen.Degraded != "" {
		parts = append(parts, "note: "+screen.Degraded)
	}
	switch {
	case screen.Output == "" && screen.Mode == "delta":
		parts = append(parts, "nothing new since your last read")
	case screen.Output == "":
		parts = append(parts, "session screen is empty")
	case screen.Mode == "delta":
		parts = append(parts, "since your last read:\n"+screen.Output)
	default:
		parts = append(parts, "screen:\n"+screen.Output)
	}
	if screen.Cursor != "" {
		parts = append(parts, "cursor: "+screen.Cursor)
	}
	return strings.Join(parts, "\n\n")
}

func FormatSendResult(result SendResult, targetID string) string {
	if result.Relayed && result.MessageID == 0 {
		return fmt.Sprintf("relayed; the extension that launched this session took it, and nothing was queued for session %s", targetID)
	}
	text := fmt.Sprintf("queued message %d for session %s at position %d", result.MessageID, targetID, result.QueuePosition)
	if result.Superseded == 1 {
		text += ", replacing one of yours still queued on the same subject"
	} else if result.Superseded > 1 {
		text += fmt.Sprintf(", replacing %d of yours still queued on the same subject", result.Superseded)
	}
	if !result.ManagerAwake {
		text += "; Gate Inbox is not running, so it waits until the user opens it"
	}
	// A queued message is one the recipient has not seen, and a sender that
	// reads the line above as a handoff tells its user the work was passed
	// on when nothing has reached the other agent yet.
	if result.Interrupt {
		text += "; its current turn is interrupted first unless a dialog is showing"
	}
	text += ". It has not reached the agent yet; message_status says delivered once it is in the agent's prompt"
	for _, handled := range result.Handled {
		text += fmt.Sprintf(". Extension %s: %s", handled.Extension, handled.Result)
	}
	// Last, because it is the part a sender has to act on: everything above
	// says the send worked, and this says the message is not going anywhere
	// yet and what would move it.
	if result.Held != "" {
		text += ". It is held rather than waiting its turn: " + result.Held
	}
	return text
}

func FormatMessageState(state MessageState) string {
	text := fmt.Sprintf("message %d to session %s is %s", state.MessageID, state.SessionID, state.State)
	if state.Interrupt {
		text += " (sent with interrupt)"
	}
	if state.DeliveredAt != "" {
		text += " (delivered " + state.DeliveredAt + ")"
	}
	if state.Reason != "" {
		text += ": " + state.Reason
	}
	return text
}

func FormatWaitResult(result WaitResult) string {
	text := fmt.Sprintf("%s is %s after %s", FormatSession(result.Session), result.Session.Status, result.Waited)
	switch result.Outcome {
	case WaitTimedOut:
		text = "timed out: " + text
	case WaitDied:
		text = "the session died before reaching any awaited state: " + text
	}
	if !result.ManagerAwake {
		text += "; Gate Inbox is not running, so this status is the last one it recorded"
	}
	if len(result.Standing) < 2 {
		return text
	}
	// The rest of the set, in one answer. A parent told only that child
	// three finished has to spend a list to learn what the other four are
	// doing, which is the cost this whole call exists to remove.
	reached, working, died := 0, 0, 0
	for _, standing := range result.Standing {
		switch standing.Outcome {
		case WaitReached:
			reached++
		case WaitDied:
			died++
		default:
			working++
		}
	}
	text += fmt.Sprintf("\n%d of %d reached, %d still working, %d died", reached, len(result.Standing), working, died)
	for _, standing := range result.Standing {
		text += fmt.Sprintf("\n- [%s] %s is %s", standing.Outcome, FormatSession(standing.Session), standing.Session.Status)
	}
	return text
}

func FormatTask(task Task) string {
	line := fmt.Sprintf("%s (%s) [%s]", task.Title, task.ID, task.State)
	if task.OwnerName != "" {
		line += " held by " + task.OwnerName
	}
	if task.Blocked {
		line += " blocked on " + strings.Join(task.BlockedBy, ", ")
	}
	return line
}

func FormatTaskList(tasks []Task) string {
	if len(tasks) == 0 {
		return "no tasks on the shared list"
	}
	lines := make([]string, 0, len(tasks))
	for _, task := range tasks {
		line := "- " + FormatTask(task)
		if task.Mine {
			line += "; yours"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatReservation(reservation Reservation) string {
	line := fmt.Sprintf("%s (%s) held by %s for %s", reservation.Pattern, reservation.Mode, reservation.Holder, reservation.ExpiresIn)
	if reservation.Note != "" {
		line += ": " + reservation.Note
	}
	return line
}

func FormatReservations(reservations []Reservation) string {
	if len(reservations) == 0 {
		return "no files are reserved"
	}
	lines := make([]string, 0, len(reservations))
	for _, reservation := range reservations {
		line := "- " + FormatReservation(reservation)
		if reservation.Mine {
			line += "; yours"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatReserveResult(result ReserveResult) string {
	lines := make([]string, 0, len(result.Reserved)+len(result.Conflicts)+1)
	for _, reservation := range result.Reserved {
		lines = append(lines, "reserved "+reservation.Pattern)
	}
	if len(result.Conflicts) > 0 {
		lines = append(lines, "conflicts with leases already held; message the holder before editing:")
		for _, conflict := range result.Conflicts {
			lines = append(lines, "- "+FormatReservation(conflict))
		}
	}
	return strings.Join(lines, "\n")
}

func FormatReleased(count int) string {
	return fmt.Sprintf("released %d reservation(s)", count)
}

func FormatGroupRemoval(removal GroupRemoval) string {
	line := fmt.Sprintf("deleted %s", strings.Join(removal.Removed, ", "))
	if len(removal.Moved) > 0 {
		line += fmt.Sprintf("; %d session(s) moved to the root group: %s",
			len(removal.Moved), strings.Join(removal.Moved, ", "))
	}
	return line
}

func FormatGroupList(groups []Group) string {
	if len(groups) == 0 {
		return "no groups; sessions live in the root"
	}
	lines := make([]string, 0, len(groups))
	for _, group := range groups {
		line := fmt.Sprintf("- %s; sessions=%d", group.Path, group.Sessions)
		if group.Directory != "" {
			line += "; directory=" + group.Directory
		}
		if group.Archived {
			line += "; archived"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatParkResult(result ParkResult) string {
	verb := "parked"
	if result.DryRun {
		verb = "dry run: would park"
	}
	lines := []string{fmt.Sprintf("%s %d session(s); unpark will revive %d", verb, len(result.Parked), result.Owed)}
	for _, session := range result.Parked {
		lines = append(lines, "- "+FormatSession(session))
	}
	if result.Promoted > 0 {
		lines = append(lines, fmt.Sprintf("%d of those were adopted panes, now owned rows: unpark restarts them as gi_ sessions, not in the windows they borrowed", result.Promoted))
	}
	if result.Recovered > 0 {
		lines = append(lines, fmt.Sprintf("%d of those were gi_ sessions with no board row and were given one", result.Recovered))
	}
	if len(result.Orphans) > 0 {
		lines = append(lines, fmt.Sprintf("left %d gi_ session(s) running whose agent could not be identified, so nothing records them for unpark: %s",
			len(result.Orphans), strings.Join(result.Orphans, ", ")))
	}
	if result.Terminals > 0 {
		lines = append(lines, fmt.Sprintf("left %d terminal(s) running: a shell has nothing to resume", result.Terminals))
	}
	if result.Self {
		lines = append(lines, "left this session running")
	}
	if len(result.Working) > 0 {
		lines = append(lines, fmt.Sprintf("%d stopped mid-turn; unpark tells them to continue: %s", len(result.Working), strings.Join(result.Working, ", ")))
	}
	for _, warning := range result.Warnings {
		lines = append(lines, "warning: "+warning)
	}
	for _, failure := range result.Errors {
		lines = append(lines, "failed: "+failure)
	}
	return strings.Join(lines, "\n")
}

func FormatUnparkResult(result UnparkResult) string {
	lines := []string{fmt.Sprintf("revived %d session(s); %d still parked", len(result.Revived), result.Owed)}
	for _, session := range result.Revived {
		lines = append(lines, "- "+FormatSession(session))
	}
	if len(result.Nudged) > 0 {
		lines = append(lines, fmt.Sprintf("%d were mid-turn when parked and were told to continue: %s", len(result.Nudged), strings.Join(result.Nudged, ", ")))
	}
	if result.Continued > 0 {
		lines = append(lines, fmt.Sprintf("%d revived with the tool's continue command: no conversation id was captured, so they may resume the wrong conversation", result.Continued))
	}
	if result.AlreadyRunning > 0 {
		lines = append(lines, fmt.Sprintf("%d already running, dropped from the set", result.AlreadyRunning))
	}
	if result.Gone > 0 {
		lines = append(lines, fmt.Sprintf("%d deleted or archived since, dropped from the set", result.Gone))
	}
	for _, failure := range result.Errors {
		lines = append(lines, "failed, kept for a retry: "+failure)
	}
	return strings.Join(lines, "\n")
}

// FormatAnswer says what the answer did, in one line: the option it landed
// on, or that it was typed. A caller that picked an option nobody offered
// gets to see that its words went in as words.
func FormatAnswer(answered AnsweredQuestion) string {
	line := fmt.Sprintf("answered %s (%s) by typing", answered.Name, answered.SessionID)
	if answered.Selected != "" {
		line = fmt.Sprintf("answered %s (%s) with option %q", answered.Name, answered.SessionID, answered.Selected)
	}
	if answered.Standing > 0 {
		line += fmt.Sprintf("; %d more question(s) in the same dialog still stand", answered.Standing)
	}
	return line
}

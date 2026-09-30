package dialog

import "strings"

// ApprovalHeader is the header a child gives the AskUserQuestion it raises to
// get its user's approval for one action. Only an answer relayed from the
// user's own dialog is keyed into one (sessioncmd's relay.go).
const ApprovalHeader = "Approval"

// IsApproval reports whether header marks an approval question.
func IsApproval(header string) bool {
	return strings.EqualFold(strings.TrimSpace(header), ApprovalHeader)
}

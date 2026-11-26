package extension

import "context"

// ActiveWork describes an item held in a distribution's shared work registry.
// Key and Description can be sent to the board's selected classifier. Session
// is used locally to exclude the requesting session's own work.
type ActiveWork struct {
	Key         string
	Description string
	Session     string
}

// ActiveWorkSource reads current work without creating or updating claims.
// It runs from the hook process under the launch check's deadline.
type ActiveWorkSource func(context.Context) ([]ActiveWork, error)

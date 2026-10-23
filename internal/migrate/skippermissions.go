package migrate

import (
	"fmt"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

// SkipPermissions is how the moved session runs: the source's two settings,
// each replaced where the move names its own (nil keeps the source's). The
// session's own setting kept from a source is dropped on a CLI with no flag
// to honour it, since that brings the prompts back rather than refusing the
// move; asked for outright on such a CLI, it is refused.
func SkipPermissions(source store.Session, tool config.Tool, self, children *bool) (bool, bool, error) {
	skip := source.SkipPermissions && tool.SkipPermissionsFlag != ""
	if self != nil {
		skip = *self
	}
	if skip && tool.SkipPermissionsFlag == "" {
		return false, false, fmt.Errorf("%s cannot be launched with permissions skipped: its config names no "+
			"skip_permissions_flag; move it with skip permissions off", tool.Command)
	}
	childrenSkip := source.ChildrenSkipPermissions
	if children != nil {
		childrenSkip = *children
	}
	return skip, childrenSkip, nil
}

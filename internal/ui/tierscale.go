package ui

import (
	"fmt"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

// The priority key cycles a scale the operator chooses: up to three tiers
// above the default and up to three below. Fewer tiers are quicker to
// cycle and easier to keep apart, so the default is two above and one
// below. A stored tier past the chosen scale reads as its edge (see
// priority.Scale.Clamp) and keeps its stored value, so narrowing the scale
// and widening it again loses nothing.

const priorityScaleSetting = "priority_scale"

// priorityScaleModes is every scale the settings row steps through, in the
// stored "+R/-L" form, fewest tiers first.
var priorityScaleModes = func() []string {
	var modes []string
	for raised := 1; raised <= priority.MaxSteps; raised++ {
		for lowered := 0; lowered <= priority.MaxSteps; lowered++ {
			modes = append(modes, formatPriorityScale(priority.Scale{Raised: raised, Lowered: lowered}))
		}
	}
	return modes
}()

func formatPriorityScale(s priority.Scale) string {
	s = s.Normalize()
	return fmt.Sprintf("+%d/-%d", s.Raised, s.Lowered)
}

// parsePriorityScale reads a stored scale; anything unreadable is the
// default rather than an error, so a hand-edited value cannot break the key.
func parsePriorityScale(text string) priority.Scale {
	var s priority.Scale
	if _, err := fmt.Sscanf(text, "+%d/-%d", &s.Raised, &s.Lowered); err != nil {
		return priority.DefaultScale
	}
	if formatPriorityScale(s) != text {
		return priority.DefaultScale
	}
	return s
}

func normalizePriorityScale(text string) string {
	return formatPriorityScale(parsePriorityScale(text))
}

func storedPriorityScale(st *store.Store) string {
	chosen, err := st.Setting(priorityScaleSetting)
	if err != nil {
		return formatPriorityScale(priority.DefaultScale)
	}
	return normalizePriorityScale(chosen)
}

// scale is the tier scale the board is using now.
func (m *Model) scale() priority.Scale { return parsePriorityScale(m.priorityScale) }

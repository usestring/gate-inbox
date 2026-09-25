package extensionhost

import (
	"context"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// A role is checked before anything is launched, and one that could pass for
// another extension's qualified role is refused rather than recorded.
func TestLaunchForRefusesARoleThatIsNotOneWord(t *testing.T) {
	board := NewEvents(NewBoard("", nil), nil)
	host, release := board.For("ext1")
	defer release()
	for _, role := range []string{"other/reviewer", "Reviewer", "1st", "has space"} {
		_, err := host.Launch(context.Background(), extension.LaunchRequest{Tool: "t", Role: role})
		if err == nil || !strings.Contains(err.Error(), "must be lower case") {
			t.Errorf("Launch with role %q = %v, want it refused", role, err)
		}
	}
}

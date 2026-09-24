package decline_test

import (
	"testing"

	"github.com/usestring/gate-inbox/extension/decline"
)

func TestLooksLikeTellsARefusalFromAFinding(t *testing.T) {
	if !decline.LooksLike("Sorry, I can't help with that request.") {
		t.Error("a refusal of the work was let through")
	}
	if decline.LooksLike("I can't reproduce the failure on main.") {
		t.Error("a finding was read as a refusal")
	}
}

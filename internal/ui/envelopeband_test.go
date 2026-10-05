package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/store"
)

// The transcript parser tells the board's own typing from the user's by the
// band alone, so every envelope must open with it: a keep-warm that did not
// would be read back as the session's task and the row renamed after it.
func TestEveryEnvelopeOpensWithTheBand(t *testing.T) {
	cases := map[string]string{
		"extension": store.ExtensionSenderID("cache-warden"),
		"system":    store.SystemSenderID,
		"agent":     "7c1d90ab",
	}
	for name, sender := range cases {
		for _, taught := range []bool{false, true} {
			msg := typicalMessage()
			msg.SenderID = sender
			if got := inboxEnvelope(msg, "claude", taught, messageContext{}); !band.Has(got) {
				t.Errorf("%s (taught %v) envelope does not open with %q: %.80q", name, taught, band.Tag, got)
			}
		}
	}
}

package forge

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/workspec"
)

func TestNewResolversBuildsOnlyWhatIsOn(t *testing.T) {
	cases := []struct {
		name        string
		p           Providers
		github, lin bool
	}{
		{"both on", Providers{GitHub: true, Linear: true, LinearAPIKey: "k"}, true, true},
		{"github off", Providers{Linear: true, LinearAPIKey: "k"}, false, true},
		{"linear off", Providers{GitHub: true, LinearAPIKey: "k"}, true, false},
		{"linear on without a key", Providers{GitHub: true, Linear: true}, true, true},
		{"both off", Providers{}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prs, tickets := NewResolvers(c.p)
			if (prs != nil) != c.github {
				t.Errorf("pull request resolver built = %v, want %v", prs != nil, c.github)
			}
			if (tickets != nil) != c.lin {
				t.Errorf("ticket resolver built = %v, want %v", tickets != nil, c.lin)
			}
		})
	}
}

type countingTransport struct{ requests int }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.requests++
	return nil, errors.New("no network in tests")
}

// A Linear with no key sends nothing, says it is off rather than failed, and says why.
func TestLinearWithoutAKeyIsOffNotFailed(t *testing.T) {
	transport := &countingTransport{}
	l := &Linear{Endpoint: "https://example.invalid", HTTP: &http.Client{Transport: transport}}

	_, health := l.Tickets([]workspec.Ref{{Kind: workspec.KindTicket, Identifier: "S-1"}})

	if transport.requests != 0 {
		t.Errorf("sent %d requests with no key", transport.requests)
	}
	if !health.Off || health.Failed() {
		t.Errorf("health = %+v, want off and not failed", health)
	}
	if !strings.Contains(health.Reason, "LINEAR_API_KEY is not set") {
		t.Errorf("reason = %q, want it to name the missing key", health.Reason)
	}
}

// Built with Linear on and no key, the resolver is there and keeps the empty key rather than
// picking one up from the environment later.
func TestNewResolversKeepsAKeylessLinear(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "from-env")
	_, tickets := NewResolvers(Providers{Linear: true})
	linear, ok := tickets.(*Linear)
	if !ok {
		t.Fatalf("ticket resolver = %T, want *Linear", tickets)
	}
	if linear.APIKey != "" {
		t.Errorf("key = %q, want the empty key it was given", linear.APIKey)
	}
}

package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/workspec"
)

// linearDefaultPage is what Linear returns when a query does not say: fifty
// issues, with nothing in the answer to say more were matched.
const linearDefaultPage = 50

// linearStub answers a ticket query the way Linear does -- at most `first`
// issues, or fifty when the query did not ask -- and never says it truncated.
type linearStub struct {
	requests []map[string]any
}

func (s *linearStub) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, decoded.Variables)

	key, _ := decoded.Variables["key"].(string)
	numbers, _ := decoded.Variables["numbers"].([]any)
	page := linearDefaultPage
	if asked, ok := decoded.Variables["first"].(float64); ok {
		page = int(asked)
	}
	first := len(numbers)
	if page < first {
		first = page
	}

	var nodes []string
	for _, number := range numbers[:first] {
		nodes = append(nodes, fmt.Sprintf(
			`{"identifier":"%s-%d","title":"t","url":"u","state":{"name":"Done","type":"completed"},"assignee":{"displayName":"a"}}`,
			key, int(number.(float64))))
	}
	payload := `{"data":{"issues":{"nodes":[` + strings.Join(nodes, ",") + `]}}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(payload))),
		Header:     http.Header{},
	}, nil
}

func ticketRefs(key string, from, count int) []workspec.Ref {
	refs := make([]workspec.Ref, 0, count)
	for i := 0; i < count; i++ {
		refs = append(refs, workspec.Ref{
			Kind:       workspec.KindTicket,
			Identifier: fmt.Sprintf("%s-%d", key, from+i),
		})
	}
	return refs
}

// Linear pages `issues` and never says it truncated, so an answer covering
// fifty of seventy references is a healthy answer that quietly loses twenty.
// Every request has to ask for exactly as many issues as it names.
func TestEveryTicketAskedAboutIsAskedForInFull(t *testing.T) {
	stub := &linearStub{}
	linear := &Linear{APIKey: "k", Endpoint: "http://linear.test",
		HTTP: &http.Client{Transport: stub}}

	refs := ticketRefs("S", 100000, 170)
	tickets, health := linear.Tickets(refs)
	if !health.OK {
		t.Fatalf("health = %+v", health)
	}
	if len(tickets) != len(refs) {
		t.Fatalf("resolved %d of %d tickets", len(tickets), len(refs))
	}
	for _, variables := range stub.requests {
		numbers, _ := variables["numbers"].([]any)
		first, _ := variables["first"].(float64)
		if int(first) != len(numbers) {
			t.Errorf("asked for %v issues while naming %d", first, len(numbers))
		}
		if len(numbers) > teamPage {
			t.Errorf("one request named %d numbers, over the %d page", len(numbers), teamPage)
		}
	}
}

// A team small enough to fit is still one request; the chunking must not turn
// the ordinary case into several.
func TestASmallTeamIsOneRequest(t *testing.T) {
	stub := &linearStub{}
	linear := &Linear{APIKey: "k", Endpoint: "http://linear.test",
		HTTP: &http.Client{Transport: stub}}

	if _, health := linear.Tickets(ticketRefs("S", 1, 4)); !health.OK {
		t.Fatalf("health = %+v", health)
	}
	if len(stub.requests) != 1 {
		t.Errorf("requests = %d, want 1", len(stub.requests))
	}
}

/* --------------------------------------------------------------------- staying under the quota */

// refusingAfter answers normally for the first n requests and refuses everything after.
type refusingAfter struct {
	inner    *linearStub
	after    int
	status   int
	retryFor string
}

func (r *refusingAfter) RoundTrip(request *http.Request) (*http.Response, error) {
	if len(r.inner.requests) >= r.after {
		header := http.Header{}
		if r.retryFor != "" {
			header.Set("Retry-After", r.retryFor)
		}
		return &http.Response{
			StatusCode: r.status,
			Status:     http.StatusText(r.status),
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     header,
		}, nil
	}
	return r.inner.RoundTrip(request)
}

func limitedLinear(after, status int, retryFor string) (*Linear, time.Time) {
	now := time.Unix(1700000000, 0)
	return &Linear{
		APIKey:   "k",
		Endpoint: "http://linear.test",
		HTTP: &http.Client{Transport: &refusingAfter{
			inner: &linearStub{}, after: after, status: status, retryFor: retryFor,
		}},
		Now: func() time.Time { return now },
	}, now
}

// A 429 says how long to wait. Ignoring it and re-asking on the next thirty-second tick is how a
// limit that would have cleared in a minute becomes one that lasts the hour.
func TestLinearWaitsAsLongAsItWasAskedTo(t *testing.T) {
	linear, now := limitedLinear(0, http.StatusTooManyRequests, "90")

	_, health := linear.Tickets(ticketRefs("S", 1, 3))
	if health.OK {
		t.Fatal("a rate-limited look reported healthy")
	}
	if want := now.Add(90 * time.Second); !health.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", health.RetryAfter, want)
	}
}

// A 429 with no Retry-After still has to stand down; zero would mean "ask again immediately".
func TestASilentRateLimitStillBacksOff(t *testing.T) {
	linear, now := limitedLinear(0, http.StatusTooManyRequests, "")

	_, health := linear.Tickets(ticketRefs("S", 1, 3))
	if want := now.Add(linearBackoff); !health.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", health.RetryAfter, want)
	}
}

// A rejected key will not be accepted by asking again in thirty seconds.
func TestARejectedKeyIsLeftAloneForAWhile(t *testing.T) {
	linear, now := limitedLinear(0, http.StatusUnauthorized, "")

	_, health := linear.Tickets(ticketRefs("S", 1, 3))
	if want := now.Add(credentialBackoff); !health.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", health.RetryAfter, want)
	}
}

// Pages that answered are kept. Throwing them away because a later one was refused means every
// ticket on the board comes due again on the next tick — more requests, in exactly the moment the
// source has just said it wants fewer.
func TestPagesThatAnsweredSurviveALaterRefusal(t *testing.T) {
	linear, _ := limitedLinear(1, http.StatusTooManyRequests, "30")

	refs := ticketRefs("S", 1, teamPage+10)
	tickets, health := linear.Tickets(refs)

	if health.OK {
		t.Fatal("a refused page reported healthy")
	}
	if len(tickets) != teamPage {
		t.Errorf("kept %d tickets of the %d that answered", len(tickets), teamPage)
	}
	if len(health.Answered) != teamPage {
		t.Errorf("marked %d references answered, want the %d whose page came back",
			len(health.Answered), teamPage)
	}
}

// The identifier a reference settles under is the one it was written with. Linear filters on a
// number, so rebuilding "ABC-0042" from the 42 that went out would settle "ABC-42" instead and leave
// the real reference due on every tick.
func TestAZeroPaddedIdentifierSettlesUnderItsOwnKey(t *testing.T) {
	stub := &linearStub{}
	linear := &Linear{APIKey: "k", Endpoint: "http://linear.test",
		HTTP: &http.Client{Transport: stub}}

	padded := workspec.Ref{Kind: workspec.KindTicket, Identifier: "ABC-0042"}
	_, health := linear.Tickets([]workspec.Ref{padded})
	if !health.Answered[padded.Key()] {
		t.Errorf("answered = %v, want it to settle %s", health.Answered, padded.Key())
	}
}

// A healthy look settles every identifier it named, including the ones Linear had nothing for.
// workspec's pattern matches anything shaped like ABC-123, so most boards carry a few of those
// permanently, and they must not be re-asked on every tick.
func TestTicketsThatResolveToNothingAreStillAnswered(t *testing.T) {
	stub := &linearStub{}
	linear := &Linear{APIKey: "k", Endpoint: "http://linear.test",
		HTTP: &http.Client{Transport: stub}}

	// CVE-2024 is a team key of its own that resolves to nothing real, which is precisely the
	// case that matters: it is a reference the board will carry forever.
	refs := append(ticketRefs("S", 1, 2),
		workspec.Ref{Kind: workspec.KindTicket, Identifier: "CVE-2024"})

	_, health := linear.Tickets(refs)
	if !health.OK {
		t.Fatalf("health = %+v", health)
	}
	for _, ref := range refs {
		if !health.Answered[ref.Key()] {
			t.Errorf("%s not marked answered, so it comes due again on the next tick", ref.Key())
		}
	}
}

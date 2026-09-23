package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/workspec"
)

const linearEndpoint = "https://api.linear.app/graphql"

// Linear reads ticket state through Linear's GraphQL API.
//
// Unlike GitHub there is no CLI to borrow authentication from, so this takes the LINEAR_API_KEY the
// operator already has in their environment. A missing key is a Health state, not a failure: an
// inbox on a machine with no Linear access should show pull requests and say plainly that it cannot
// see tickets.
type Linear struct {
	APIKey   string
	Endpoint string
	HTTP     *http.Client
	Now      func() time.Time
}

func NewLinear() *Linear {
	return &Linear{
		APIKey:   os.Getenv("LINEAR_API_KEY"),
		Endpoint: linearEndpoint,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		Now:      time.Now,
	}
}

func (l *Linear) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// byTeamNumbers asks for a whole team's worth of tickets in one request.
//
// Linear filters issues by team key and number, not by the human-readable identifier, so an
// "ABC-133756" is split back into its parts to ask. Batched by team for the same reason the GitHub
// side batches by repository: this runs on a ticker against however many tickets the fleet has
// open, and one round-trip per ticket would be absurd.
const byTeamNumbers = `query($key: String!, $numbers: [Float!], $first: Int!) {
	issues(first: $first, filter: { team: { key: { eq: $key } }, number: { in: $numbers } }) {
		nodes { identifier title url state { name type } assignee { displayName } }
	}
}`

// teamPage is one request's worth of one team's ticket numbers, and the page
// size asked for alongside it.
//
// Linear pages `issues` at fifty unless told otherwise, and says nothing about
// having truncated: the answer to a request for sixty is a perfectly healthy
// answer about fifty of them. Measured against this machine's fleet that lost
// sixteen live tickets of seventy-six, and they were indistinguishable from
// the ticket-shaped prose that legitimately resolves to nothing. Asking for
// exactly as many as are sent is what makes a short answer mean the tickets
// are not there.
const teamPage = 100

// teamNumber is one ticket in the form Linear's filter takes, beside the identifier it came from.
type teamNumber struct {
	identifier string
	number     float64
}

// splitIdentifier takes "ABC-133756" apart into the team key and number Linear filters on.
func splitIdentifier(identifier string) (string, float64, bool) {
	key, digits, found := strings.Cut(identifier, "-")
	if !found || key == "" {
		return "", 0, false
	}
	number, err := strconv.ParseFloat(digits, 64)
	if err != nil {
		return "", 0, false
	}
	return key, number, true
}

type ticketNode struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Assignee struct {
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
}

func (l *Linear) post(query string, variables map[string]any) ([]ticketNode, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest(http.MethodPost, l.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", l.APIKey)

	client := l.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, errUnauthorized
	}
	// Linear answers an exhausted quota with a 429 and, usually, how long to wait. Honouring it
	// is the difference between standing down for the minute it asks for and re-asking twice a
	// minute for the rest of the hour — which is how a brief limit becomes a permanent one.
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, rateLimitError{after: retryAfterHeader(response.Header)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errStatus(response.Status)
	}

	var decoded struct {
		Data struct {
			Issues struct {
				Nodes []ticketNode `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded.Data.Issues.Nodes, nil
}

type forgeError string

func (e forgeError) Error() string { return string(e) }

const errUnauthorized = forgeError("unauthorized")

func errStatus(status string) error { return forgeError("linear: " + status) }

// rateLimitError is a refusal over quota rather than over the request, carrying how long the
// source asked to be left alone for.
type rateLimitError struct{ after time.Duration }

func (rateLimitError) Error() string { return "linear: rate limited" }

// linearBackoff is how long to wait when a 429 arrives without saying.
const linearBackoff = time.Minute

// retryAfterHeader reads the delay Linear asked for, in either form the header takes: a count of
// seconds, or an HTTP date. An unreadable one falls back to a wait that is at least not zero.
func retryAfterHeader(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return linearBackoff
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}
	return linearBackoff
}

// Tickets resolves every ticket reference.
//
// Anything not found is simply absent from the map, which a caller draws as unresolved rather than
// as an error. That is the normal outcome for a ticket-shaped token spotted in prose: workspec's
// pattern matches anything of the form ABC-123, and plenty of those name nothing at all. Health's
// Answered is what says that absence was an answer, so those references settle instead of coming
// due on every tick forever.
//
// Pages are kept as they arrive. A team whose fourth page is refused does not undo the three that
// answered: those tickets are resolved, they are recorded as answered, and only the rest come back.
func (l *Linear) Tickets(refs []workspec.Ref) (map[string]Ticket, Health) {
	wanted := map[string]bool{}
	for _, ref := range refs {
		if ref.Kind == workspec.KindTicket && ref.Identifier != "" {
			wanted[ref.Identifier] = true
		}
	}
	if len(wanted) == 0 {
		return map[string]Ticket{}, Health{OK: true, Answered: map[string]bool{}}
	}
	if l.APIKey == "" {
		// No key is a fixed no until the operator restarts with one, so there is nothing to
		// come back for. Nothing is spent either way; the wait keeps the header honest
		// rather than flickering.
		return nil, Health{
			Off:        true,
			Reason:     "LINEAR_API_KEY is not set — ticket state is unavailable",
			RetryAfter: l.now().Add(credentialBackoff),
		}
	}

	// Grouped by team, because that is the axis Linear's filter takes. A fleet working one
	// team's tickets is therefore one request, which is the usual case. Each number keeps the
	// identifier it came from: rebuilding one from the number would not survive the round trip
	// through Linear's float filter, and "ABC-0042" would settle the key "ABC-42" instead.
	byTeam := map[string][]teamNumber{}
	for identifier := range wanted {
		if key, number, ok := splitIdentifier(identifier); ok {
			byTeam[key] = append(byTeam[key], teamNumber{identifier: identifier, number: number})
		}
	}

	at := l.now()
	resolved := map[string]Ticket{}
	answered := map[string]bool{}
	var nodes []ticketNode
	var failed Health
pages:
	for key, numbers := range byTeam {
		for len(numbers) > 0 {
			page := numbers
			if len(page) > teamPage {
				page = page[:teamPage]
			}
			numbers = numbers[len(page):]
			asked := make([]float64, len(page))
			for i, entry := range page {
				asked[i] = entry.number
			}
			found, err := l.post(byTeamNumbers, map[string]any{
				"key": key, "numbers": asked, "first": len(asked),
			})
			if err != nil {
				failed = l.postFailure(err)
				break pages
			}
			nodes = append(nodes, found...)
			// The page came back, so every identifier it named is settled — the
			// ones it did not mention are settled as not existing, which is the
			// answer for most of what workspec's pattern picks out of prose.
			for _, entry := range page {
				ref := workspec.Ref{Kind: workspec.KindTicket, Identifier: entry.identifier}
				answered[ref.Key()] = true
			}
		}
	}

	for _, node := range nodes {
		if !wanted[node.Identifier] {
			continue
		}
		key := workspec.Ref{Kind: workspec.KindTicket, Identifier: node.Identifier}.Key()
		resolved[key] = Ticket{
			Identifier: node.Identifier,
			Title:      node.Title,
			State:      node.State.Name,
			StateType:  node.State.Type,
			Assignee:   node.Assignee.DisplayName,
			URL:        node.URL,
			FetchedAt:  at,
		}
	}

	if failed.Failed() {
		// Whatever answered still counts. Returning nothing here is what made a single
		// bad minute re-ask for every ticket on the board on the very next tick.
		failed.Answered = answered
		return resolved, failed
	}
	return resolved, Health{OK: true, Answered: answered}
}

// postFailure turns a refused request into a Health that says when to come back.
func (l *Linear) postFailure(err error) Health {
	var limited rateLimitError
	switch {
	case errors.As(err, &limited):
		return Health{Reason: "Linear rate limit reached", RetryAfter: l.now().Add(limited.after)}
	case errors.Is(err, errUnauthorized):
		return Health{Reason: "Linear rejected the API key", RetryAfter: l.now().Add(credentialBackoff)}
	default:
		return Health{Reason: err.Error()}
	}
}

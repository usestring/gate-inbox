// Package workspec decides what a session is working on: which pull requests and which tickets,
// and on what evidence.
//
// v1 had the regexes and applied them to the launch prompt alone, rendering the result as a dead
// string on the row. This keeps the patterns and changes two things: the input widens to anything
// the session said, and every reference records how it was found. Provenance is not decoration —
// a ticket id taken from the branch a session is committing to is a fact, and the same id spotted
// in prose is a mention. The board shows live state for both, so it has to be able to say which.
package workspec

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind is what sort of thing a reference names.
type Kind string

const (
	KindPR     Kind = "pr"
	KindTicket Kind = "ticket"
)

// Provenance is how a reference was found, strongest first.
type Provenance string

const (
	// FromBranch is the checked-out branch of the session's own working directory.
	FromBranch Provenance = "branch"
	// FromCreation is the session watching itself open a pull request.
	FromCreation Provenance = "pr-create"
	// FromText is a mention anywhere the session or its operator wrote.
	FromText Provenance = "text"
)

// rank orders provenance so the strongest evidence for a reference survives deduplication.
var rank = map[Provenance]int{FromBranch: 3, FromCreation: 2, FromText: 1}

// Ref is one thing a session is working on.
type Ref struct {
	Kind Kind
	// Repo is "owner/name" for a pull request, empty for a ticket.
	Repo string
	// Number is the pull request number, 0 for a ticket.
	Number int
	// Identifier is the ticket key, e.g. "ABC-100002". Empty for a pull request.
	Identifier string
	Provenance Provenance
	// Inferred marks a pull request whose repository nobody stated: the text said "PR #39"
	// and the repository came from the session's working directory. The number is evidence;
	// the repository is a guess, and PruneInferred is what decides whether the guess stands.
	Inferred bool
}

// Key identifies a reference independently of how it was found, so the same PR spotted three
// ways collapses to one row.
func (r Ref) Key() string {
	if r.Kind == KindTicket {
		return "ticket:" + r.Identifier
	}
	return "pr:" + r.Repo + "#" + strconv.Itoa(r.Number)
}

var (
	ticketRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]{0,9}-\d+)\b`)
	pullURL  = regexp.MustCompile(`(?i)https?://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)`)
	// barePull is the weakest pattern by far — "#7394" alone is a number in prose as often as
	// it is a pull request, so it requires the word.
	barePull = regexp.MustCompile(`(?i)\b(?:PR|pull request)\s*#?(\d+)\b`)
	// namedPull is the shorthand a superproject's operators write: "go#1392", the submodule
	// path and the number. Only a name Discover can place — a submodule, or the repository
	// itself — turns one into a reference; see ScanNamed.
	namedPull = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_.-]*)#(\d+)\b`)
	// linearBranch is Linear's own branch format: <handle>/<team>-<number>-<slug>.
	linearBranch = regexp.MustCompile(`(?i)(?:^|/)([a-z][a-z0-9]{0,9}-\d+)(?:-|$)`)
	// sshRemote and httpsRemote pull "owner/name" out of the two forms a git remote takes.
	sshRemote   = regexp.MustCompile(`^(?:ssh://)?git@[^:/]+[:/]([^/]+)/(.+?)(?:\.git)?$`)
	httpsRemote = regexp.MustCompile(`^https?://[^/]+/([^/]+)/(.+?)(?:\.git)?/?$`)
)

// RepoFromRemote reduces a git remote URL to "owner/name", or empty when it is neither form.
func RepoFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	for _, re := range []*regexp.Regexp{sshRemote, httpsRemote} {
		if m := re.FindStringSubmatch(remote); m != nil {
			return m[1] + "/" + m[2]
		}
	}
	return ""
}

// TicketFromBranch reads the ticket a Linear-format branch names.
//
// This is the strongest evidence there is that a session is working a ticket: the operator or the
// agent checked that branch out to do the work, which no amount of talking about a ticket implies.
func TicketFromBranch(branch string) (Ref, bool) {
	m := linearBranch.FindStringSubmatch(branch)
	if m == nil {
		return Ref{}, false
	}
	return Ref{
		Kind:       KindTicket,
		Identifier: strings.ToUpper(m[1]),
		Provenance: FromBranch,
	}, true
}

// ScanText finds every reference in a body of text the session produced.
//
// repo is the session's own repository, used to resolve a bare "PR #7394". When it is unknown
// such a mention is dropped rather than attached to a guess: a confidently wrong pull request
// state on the board is worse than an empty column, which is the same restraint the classifier
// shows by answering "unknown" instead of inferring.
//
// A bare mention resolved against repo is marked Inferred, because the number is what the text
// said and the repository is not. PruneInferred gets the last word on those.
func ScanText(text, repo string, how Provenance) []Ref {
	var refs []Ref

	for _, m := range pullURL.FindAllStringSubmatch(text, -1) {
		refs = append(refs, Ref{
			Kind:       KindPR,
			Repo:       m[1] + "/" + strings.TrimSuffix(m[2], ".git"),
			Number:     mustAtoi(m[3]),
			Provenance: how,
		})
	}

	if repo != "" {
		for _, m := range barePull.FindAllStringSubmatch(text, -1) {
			refs = append(refs, Ref{
				Kind:       KindPR,
				Repo:       repo,
				Number:     mustAtoi(m[1]),
				Provenance: how,
				Inferred:   true,
			})
		}
	}

	for _, m := range ticketRe.FindAllStringSubmatch(text, -1) {
		refs = append(refs, Ref{
			Kind:       KindTicket,
			Identifier: m[1],
			Provenance: how,
		})
	}

	return refs
}

// ScanNamed finds pull requests written as "<name>#<number>", where name is a repository the
// session can see: the superproject, or a submodule path under it. The number came from the
// text and the repository from git, so the reference is stated rather than inferred.
//
// names maps a short name to "owner/name". A mention whose name is not in it is dropped, for
// the same reason ScanText drops a bare number without a repository.
func ScanNamed(text string, names map[string]string, how Provenance) []Ref {
	if len(names) == 0 {
		return nil
	}
	var refs []Ref
	for _, m := range namedPull.FindAllStringSubmatch(text, -1) {
		repo, ok := names[strings.ToLower(m[1])]
		if !ok {
			continue
		}
		refs = append(refs, Ref{
			Kind:       KindPR,
			Repo:       repo,
			Number:     mustAtoi(m[2]),
			Provenance: how,
		})
	}
	return refs
}

// mustAtoi converts a run of digits the regexp already matched. A number too large for an int is
// not a pull request anybody has, so it collapses to zero and Dedupe drops it.
func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// PruneInferred drops guessed pull requests that a stated one contradicts.
//
// A repository-qualified reference — a full URL, or one the session watched itself open — names
// its repository outright. When such a reference carries the same number as a bare mention, the
// bare mention is overwhelmingly the same pull request said twice, once loosely: an agent writes
// "opened PR #39" on the line above the URL it just printed. Keeping both invents a second pull
// request in whatever repository the working directory happened to point at, which under a
// superproject checkout is the wrong one and resolves anyway, so it renders as confidently as
// the real one. The stated repository wins and the guess is dropped.
//
// Order is preserved and non-pull references pass through untouched.
func PruneInferred(refs []Ref) []Ref {
	stated := map[int]bool{}
	for _, ref := range refs {
		if ref.Kind == KindPR && !ref.Inferred {
			stated[ref.Number] = true
		}
	}

	out := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind == KindPR && ref.Inferred && stated[ref.Number] {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// Dedupe collapses references naming the same thing, keeping the strongest provenance for each
// and the order in which they were first seen.
func Dedupe(refs []Ref) []Ref {
	at := map[string]int{}
	var out []Ref

	for _, ref := range refs {
		if ref.Kind == KindPR && (ref.Repo == "" || ref.Number == 0) {
			continue
		}
		if ref.Kind == KindTicket && ref.Identifier == "" {
			continue
		}

		key := ref.Key()
		index, seen := at[key]
		if !seen {
			at[key] = len(out)
			out = append(out, ref)
			continue
		}
		if rank[ref.Provenance] > rank[out[index].Provenance] {
			out[index].Provenance = ref.Provenance
		}
		if !ref.Inferred {
			out[index].Inferred = false
		}
	}

	return out
}

// ByEvidence orders references strongest evidence first, leaving the input alone.
//
// The board lists all of a session's work rather than picking one of each, so provenance stops
// being a tiebreak and becomes the order itself: the branch somebody checked out to do the work
// leads, the pull request the session watched itself open follows, and everything it merely said
// comes last. Stable, so equal evidence keeps the order it was discovered in.
func ByEvidence(refs []Ref) []Ref {
	out := append([]Ref(nil), refs...)
	sort.SliceStable(out, func(i, j int) bool {
		return rank[out[i].Provenance] > rank[out[j].Provenance]
	})
	return out
}

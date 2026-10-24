package forge

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Trigger is what put a pull request in front of the operator for review.
type Trigger string

const (
	// TriggerRequested is the operator named in the pull request's Reviewers.
	TriggerRequested Trigger = "requested"
	// TriggerLabel is the review label added to the pull request.
	TriggerLabel Trigger = "label"
	// TriggerComment is the review command written in a comment.
	TriggerComment Trigger = "comment"
)

// ReviewSearch is what counts as a request for the operator's review.
type ReviewSearch struct {
	Triggers []Trigger
	// Label and Command are searched only inside Orgs: a label name or a phrase is not the
	// operator's to claim across the whole of GitHub, so with no orgs those triggers are off.
	Label   string
	Command string
	Orgs    []string
	// Commenters, when set, is who may ask by comment. Empty is anyone who can comment.
	Commenters []string
}

// ReviewRequest is one ask for the operator's review: the pull request, and which trigger asked.
//
// TriggerID names the ask itself -- the review-request event, the labelling, the comment -- so a
// pull request asked about again is a new request rather than the old one found twice.
type ReviewRequest struct {
	Repo         string
	Number       int
	NodeID       string
	Title        string
	Author       string
	URL          string
	HeadSHA      string
	Draft        bool
	Additions    int
	Deletions    int
	ChangedFiles int
	Checks       ChecksState
	Trigger      Trigger
	TriggerID    string
	// AckSubject is the node a reaction acknowledging the ask goes on: the comment that asked,
	// or the pull request for the other triggers.
	AckSubject  string
	TriggeredAt time.Time
}

// Key is the request's identity: one pull request, one ask.
func (r ReviewRequest) Key() string {
	return fmt.Sprintf("%s#%d/%s/%s", r.Repo, r.Number, r.Trigger, r.TriggerID)
}

// searchFirst caps each search. A queue of more than this many open asks is not one an operator
// works through by hand, and the nested timelines make every node costly.
const searchFirst = 30

const reviewPRFields = `
	... on PullRequest {
		id number title url isDraft headRefOid additions deletions changedFiles updatedAt
		author { login }
		repository { nameWithOwner }
		commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
		timelineItems(last: 30, itemTypes: [REVIEW_REQUESTED_EVENT, LABELED_EVENT]) {` + reviewTimelineFields + `}
		comments(last: 50) { nodes { id body createdAt author { login } } }
	}
`

const reviewTimelineFields = `
		pageInfo { hasPreviousPage startCursor }
		nodes {
			__typename
			... on ReviewRequestedEvent { id createdAt requestedReviewer {
				__typename ... on User { login } } }
			... on LabeledEvent { id createdAt label { name } }
		}
`

type reviewPRNode struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	IsDraft      bool      `json:"isDraft"`
	HeadRefOid   string    `json:"headRefOid"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	ChangedFiles int       `json:"changedFiles"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Author       *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	TimelineItems struct {
		PageInfo struct {
			HasPreviousPage bool   `json:"hasPreviousPage"`
			StartCursor     string `json:"startCursor"`
		} `json:"pageInfo"`
		Nodes []struct {
			Typename          string    `json:"__typename"`
			ID                string    `json:"id"`
			CreatedAt         time.Time `json:"createdAt"`
			RequestedReviewer *struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
			} `json:"requestedReviewer"`
			Label *struct {
				Name string `json:"name"`
			} `json:"label"`
		} `json:"nodes"`
	} `json:"timelineItems"`
	Comments struct {
		Nodes []struct {
			ID        string    `json:"id"`
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
			Author    *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
}

// reviewQuery is one GraphQL document holding a search per enabled trigger, aliased by trigger.
func reviewQuery(search ReviewSearch) (string, bool) {
	scope := ""
	for _, org := range search.Orgs {
		if org = strings.TrimSpace(org); org != "" {
			scope += " org:" + org
		}
	}
	var parts []string
	add := func(trigger Trigger, q string) {
		parts = append(parts, fmt.Sprintf(
			`%s: search(query: %q, type: ISSUE, first: %d) { nodes { %s } }`,
			trigger, "is:open is:pr archived:false "+q, searchFirst, reviewPRFields))
	}
	for _, trigger := range search.Triggers {
		switch trigger {
		case TriggerRequested:
			add(trigger, "review-requested:@me")
		case TriggerLabel:
			if scope != "" && search.Label != "" {
				add(trigger, fmt.Sprintf("label:%q", search.Label)+scope)
			}
		case TriggerComment:
			if scope != "" && search.Command != "" {
				add(trigger, fmt.Sprintf("in:comments %q", search.Command)+scope)
			}
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return "query { rateLimit { remaining resetAt } viewer { login } " + strings.Join(parts, " ") + "}", true
}

// ReviewRequests asks GitHub for every open ask for the operator's review.
//
// A search hit is only a candidate. The comment search matches the phrase anywhere in any comment,
// so each hit is confirmed against the comments themselves: a comment counts when one of its lines
// starts with the command. A label hit carries the labelling that applied it, and a requested hit
// the request that named the operator, so the same ask is the same key across ingests.
func (g *GitHub) ReviewRequests(search ReviewSearch) ([]ReviewRequest, Health) {
	query, ok := reviewQuery(search)
	if !ok {
		return nil, Health{OK: true}
	}
	out, runErr := g.Run("api", "graphql", "-f", "query="+query)

	var response prResponse
	if json.Unmarshal(out, &response) != nil {
		if runErr != nil {
			return nil, g.failed(ghReason(runErr), rateLimitedBy(runErr), nil)
		}
		return nil, Health{Reason: "could not read gh's response"}
	}

	limited, wider := rateLimitedBy(runErr), ""
	for _, failure := range response.Errors {
		if strings.EqualFold(failure.Type, "RATE_LIMITED") {
			limited = true
		} else if wider == "" {
			wider = firstLine(failure.Message)
		}
	}
	if limited {
		return nil, g.failed("GitHub rate limit reached", true, response.budget())
	}

	var viewer struct {
		Login string `json:"login"`
	}
	_ = json.Unmarshal(response.Data["viewer"], &viewer)

	var requests []ReviewRequest
	for _, trigger := range search.Triggers {
		var result struct {
			Nodes []reviewPRNode `json:"nodes"`
		}
		if json.Unmarshal(response.Data[string(trigger)], &result) != nil {
			continue
		}
		for _, node := range result.Nodes {
			if node.Number == 0 {
				continue
			}
			asks := reviewRequestsFrom(node, trigger, search, viewer.Login)
			for len(asks) == 0 && trigger != TriggerComment && node.TimelineItems.PageInfo.HasPreviousPage {
				page, health := g.olderReviewTimeline(node)
				if !health.OK {
					return nil, health
				}
				node.TimelineItems = page.TimelineItems
				asks = reviewRequestsFrom(node, trigger, search, viewer.Login)
			}
			requests = append(requests, asks...)
		}
	}

	health := Health{OK: true}
	switch {
	case wider != "":
		health = Health{Reason: wider}
	case runErr != nil && len(response.Errors) == 0:
		return nil, g.failed(ghReason(runErr), false, response.budget())
	default:
		if budget := response.budget(); budget != nil && budget.Remaining < budgetReserve {
			health.RetryAfter = budget.ResetAt
		}
	}
	return requests, health
}

func (g *GitHub) olderReviewTimeline(node reviewPRNode) (reviewPRNode, Health) {
	cursor := node.TimelineItems.PageInfo.StartCursor
	if cursor == "" {
		return reviewPRNode{}, Health{Reason: "review timeline is missing its pagination cursor"}
	}
	query := fmt.Sprintf(`query { rateLimit { remaining resetAt } node(id: %q) {
		... on PullRequest { timelineItems(last: 100, before: %q,
			itemTypes: [REVIEW_REQUESTED_EVENT, LABELED_EVENT]) { %s } }
	} }`, node.ID, cursor, reviewTimelineFields)
	out, runErr := g.Run("api", "graphql", "-f", "query="+query)
	var response prResponse
	if err := json.Unmarshal(out, &response); err != nil {
		if runErr != nil {
			return reviewPRNode{}, g.failed(ghReason(runErr), rateLimitedBy(runErr), nil)
		}
		return reviewPRNode{}, Health{Reason: "could not read review timeline response"}
	}
	for _, failure := range response.Errors {
		if strings.EqualFold(failure.Type, "RATE_LIMITED") {
			return reviewPRNode{}, g.failed("GitHub rate limit reached", true, response.budget())
		}
	}
	if len(response.Errors) > 0 {
		return reviewPRNode{}, Health{Reason: firstLine(response.Errors[0].Message)}
	}
	if runErr != nil {
		return reviewPRNode{}, g.failed(ghReason(runErr), rateLimitedBy(runErr), response.budget())
	}
	var page reviewPRNode
	if err := json.Unmarshal(response.Data["node"], &page); err != nil || len(page.TimelineItems.Nodes) == 0 {
		return reviewPRNode{}, Health{Reason: "could not read older review timeline events"}
	}
	if page.TimelineItems.PageInfo.HasPreviousPage && (page.TimelineItems.PageInfo.StartCursor == "" || page.TimelineItems.PageInfo.StartCursor == cursor) {
		return reviewPRNode{}, Health{Reason: "review timeline pagination did not advance"}
	}
	return page, Health{OK: true}
}

// reviewRequestsFrom is the asks one search hit stands for under one trigger: none when the hit
// does not hold up, one for a request or a label, and one per asking comment.
func reviewRequestsFrom(node reviewPRNode, trigger Trigger, search ReviewSearch, viewer string) []ReviewRequest {
	base := ReviewRequest{
		Repo:         node.Repository.NameWithOwner,
		Number:       node.Number,
		NodeID:       node.ID,
		Title:        node.Title,
		URL:          node.URL,
		HeadSHA:      node.HeadRefOid,
		Draft:        node.IsDraft,
		Additions:    node.Additions,
		Deletions:    node.Deletions,
		ChangedFiles: node.ChangedFiles,
		Checks:       reviewChecks(node),
		Trigger:      trigger,
		AckSubject:   node.ID,
	}
	if node.Author != nil {
		base.Author = node.Author.Login
	}

	switch trigger {
	case TriggerRequested:
		for _, item := range node.TimelineItems.Nodes {
			if item.Typename != "ReviewRequestedEvent" || item.RequestedReviewer == nil {
				continue
			}
			who := item.RequestedReviewer
			if who.Typename != "User" || !strings.EqualFold(who.Login, viewer) {
				continue
			}
			base.TriggerID, base.TriggeredAt = item.ID, item.CreatedAt
		}
		if base.TriggerID == "" {
			return nil
		}
		return []ReviewRequest{base}
	case TriggerLabel:
		found := false
		for _, item := range node.TimelineItems.Nodes {
			if item.Typename == "LabeledEvent" && item.Label != nil && strings.EqualFold(item.Label.Name, search.Label) {
				base.TriggerID, base.TriggeredAt, found = item.ID, item.CreatedAt, true
			}
		}
		if !found {
			return nil
		}
		return []ReviewRequest{base}
	case TriggerComment:
		var out []ReviewRequest
		for _, comment := range node.Comments.Nodes {
			author := ""
			if comment.Author != nil {
				author = comment.Author.Login
			}
			if !asksForReview(comment.Body, search.Command) {
				continue
			}
			if len(search.Commenters) > 0 && !slices.ContainsFunc(search.Commenters, func(c string) bool {
				return strings.EqualFold(c, author)
			}) {
				continue
			}
			ask := base
			ask.TriggerID, ask.AckSubject, ask.TriggeredAt = comment.ID, comment.ID, comment.CreatedAt
			out = append(out, ask)
		}
		return out
	}
	return nil
}

// asksForReview is whether a comment has a line that starts with the command. A comment quoting
// the command mid-sentence, or in a reply quote, is talking about it rather than asking.
func asksForReview(body, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, command) && (len(line) == len(command) || line[len(command)] == ' ') {
			return true
		}
	}
	return false
}

func reviewChecks(node reviewPRNode) ChecksState {
	if len(node.Commits.Nodes) == 0 || node.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return ChecksNone
	}
	switch strings.ToUpper(node.Commits.Nodes[0].Commit.StatusCheckRollup.State) {
	case "SUCCESS":
		return ChecksPassing
	case "FAILURE", "ERROR":
		return ChecksFailing
	default:
		return ChecksPending
	}
}

// Acknowledge leaves an eyes reaction on each subject, so whoever asked sees the ask was picked up. One
// mutation for all of them; a reaction the viewer already left is a no-op on GitHub's side.
func (g *GitHub) Acknowledge(subjects []string) error {
	var parts []string
	for i, subject := range subjects {
		if subject == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`a%d: addReaction(input: {subjectId: %q, content: EYES}) { clientMutationId }`, i, subject))
	}
	if len(parts) == 0 {
		return nil
	}
	out, err := g.Run("api", "graphql", "-f", "query=mutation { "+strings.Join(parts, " ")+" }")
	if err != nil {
		var response prResponse
		if json.Unmarshal(out, &response) == nil && len(response.Errors) > 0 {
			return fmt.Errorf("%s", firstLine(response.Errors[0].Message))
		}
		return fmt.Errorf("%s", ghReason(err))
	}
	return nil
}

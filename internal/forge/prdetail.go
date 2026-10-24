package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PRDetail is one pull request as its GitHub page shows it: the description, the conversation,
// the review threads on its lines, its checks, and every changed file with its patch.
type PRDetail struct {
	Repo      string
	Number    int
	Title     string
	URL       string
	Body      string
	Author    string
	Draft     bool
	HeadSHA   string
	BaseRef   string
	HeadRef   string
	Commits   int
	Additions int
	Deletions int
	Checks    ChecksState
	Timeline  []PRComment
	Threads   []ReviewThread
	CheckRuns []CheckRun
	Files     []PRFile
	FetchedAt time.Time
}

// PRComment is one entry in a pull request's conversation, or one comment in a review thread.
// Review is the verdict a submitted review carried (APPROVED, CHANGES_REQUESTED, COMMENTED), and
// empty for a plain comment.
type PRComment struct {
	Author string
	Body   string
	Review string
	At     time.Time
}

// ReviewThread is a conversation pinned to one line of the diff. Side is LEFT for a line the pull
// request removes and RIGHT for one it adds or keeps, which is how GitHub tells the two line
// numbers apart. Line is zero on an outdated thread whose line is no longer in the diff.
type ReviewThread struct {
	Path     string
	Line     int
	Side     string
	Resolved bool
	Outdated bool
	Comments []PRComment
}

// CheckRun is one check on the head commit.
type CheckRun struct {
	Name  string
	State ChecksState
	// Verdict is what the check said in its own word: SUCCESS, FAILURE, SKIPPED, IN_PROGRESS.
	Verdict string
	URL     string
}

// PRFile is one changed file. Patch is empty when GitHub withholds it, for a binary file or one
// whose diff is too large to serve.
type PRFile struct {
	Path      string
	Previous  string
	Status    string
	Additions int
	Deletions int
	Patch     string
}

const prDetailQuery = `query($owner: String!, $name: String!, $number: Int!) {
	rateLimit { remaining resetAt }
	repository(owner: $owner, name: $name) { pullRequest(number: $number) {
		number title url body isDraft headRefOid baseRefName headRefName additions deletions
		author { login }
		commitCount: commits { totalCount }
		comments(first: 100) { nodes { body createdAt author { login } } }
		reviews(first: 100) { nodes { body state submittedAt author { login } } }
		reviewThreads(first: 100) { nodes { isResolved isOutdated path line diffSide
			comments(first: 50) { nodes { body createdAt author { login } } } } }
		head: commits(last: 1) { nodes { commit { statusCheckRollup { state
			contexts(first: 100) { nodes {
				__typename
				... on CheckRun { name status conclusion detailsUrl }
				... on StatusContext { context state targetUrl }
			} } } } } }
	} }
}`

type prDetailNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Body        string `json:"body"`
	IsDraft     bool   `json:"isDraft"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Author      *login `json:"author"`
	CommitCount struct {
		TotalCount int `json:"totalCount"`
	} `json:"commitCount"`
	Comments struct {
		Nodes []commentNode `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []struct {
			Body        string    `json:"body"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
			Author      *login    `json:"author"`
		} `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool   `json:"isResolved"`
			IsOutdated bool   `json:"isOutdated"`
			Path       string `json:"path"`
			Line       int    `json:"line"`
			DiffSide   string `json:"diffSide"`
			Comments   struct {
				Nodes []commentNode `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Head struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							DetailsURL string `json:"detailsUrl"`
							Context    string `json:"context"`
							State      string `json:"state"`
							TargetURL  string `json:"targetUrl"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"head"`
}

type login struct {
	Login string `json:"login"`
}

func (l *login) name() string {
	if l == nil || l.Login == "" {
		return "ghost"
	}
	return l.Login
}

type commentNode struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Author    *login    `json:"author"`
}

func (c commentNode) comment() PRComment {
	return PRComment{Author: c.Author.name(), Body: c.Body, At: c.CreatedAt}
}

type prFileNode struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

// PullRequestDetail fetches what a pull request's GitHub page shows. It is two calls: one GraphQL
// query for everything but the files, and the REST files listing, which is the only place GitHub
// serves each file's patch. Either failing fails the whole: a page with the conversation and no
// diff would read as a pull request that changes nothing.
func (g *GitHub) PullRequestDetail(repo string, number int) (*PRDetail, Health) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || number <= 0 {
		return nil, Health{Reason: "not a pull request: " + repo + "#" + strconv.Itoa(number)}
	}
	out, runErr := g.Run("api", "graphql", "-f", "query="+prDetailQuery,
		"-f", "owner="+owner, "-f", "name="+name, "-F", "number="+strconv.Itoa(number))

	var response prResponse
	if json.Unmarshal(out, &response) != nil {
		if runErr != nil {
			return nil, g.failed(ghReason(runErr), rateLimitedBy(runErr), nil)
		}
		return nil, Health{Reason: "could not read gh's response"}
	}
	for _, failure := range response.Errors {
		if strings.EqualFold(failure.Type, "RATE_LIMITED") {
			return nil, g.failed("GitHub rate limit reached", true, response.budget())
		}
	}
	var repository struct {
		PullRequest *prDetailNode `json:"pullRequest"`
	}
	if json.Unmarshal(response.Data["repository"], &repository) != nil || repository.PullRequest == nil {
		if len(response.Errors) > 0 {
			return nil, Health{Reason: firstLine(response.Errors[0].Message)}
		}
		if runErr != nil {
			return nil, g.failed(ghReason(runErr), rateLimitedBy(runErr), response.budget())
		}
		return nil, Health{Reason: repo + "#" + strconv.Itoa(number) + " was not found"}
	}

	files, health := g.pullRequestFiles(repo, number)
	if !health.OK {
		return nil, health
	}
	detail := prDetailFrom(*repository.PullRequest, repo, g.now())
	detail.Files = files
	return detail, Health{OK: true}
}

// pullRequestFiles lists every changed file with its patch. --paginate writes one JSON array per
// page back to back, which a decoder reads as successive values.
func (g *GitHub) pullRequestFiles(repo string, number int) ([]PRFile, Health) {
	out, err := g.Run("api", "--paginate", "repos/"+repo+"/pulls/"+strconv.Itoa(number)+"/files?per_page=100")
	if err != nil {
		return nil, g.failed(ghReason(err), rateLimitedBy(err), nil)
	}
	var files []PRFile
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var page []prFileNode
		if err := decoder.Decode(&page); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, Health{Reason: "could not read the pull request's files"}
		}
		for _, f := range page {
			files = append(files, PRFile{
				Path: f.Filename, Previous: f.PreviousFilename, Status: f.Status,
				Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch,
			})
		}
	}
	return files, Health{OK: true}
}

func prDetailFrom(node prDetailNode, repo string, at time.Time) *PRDetail {
	detail := &PRDetail{
		Repo: repo, Number: node.Number, Title: node.Title, URL: node.URL, Body: node.Body,
		Author: node.Author.name(), Draft: node.IsDraft, HeadSHA: node.HeadRefOid,
		BaseRef: node.BaseRefName, HeadRef: node.HeadRefName, Commits: node.CommitCount.TotalCount,
		Additions: node.Additions, Deletions: node.Deletions, Checks: ChecksNone, FetchedAt: at,
	}
	for _, c := range node.Comments.Nodes {
		detail.Timeline = append(detail.Timeline, c.comment())
	}
	// A review that only holds line comments has an empty body and says nothing in the
	// conversation beyond its verdict, so only a verdict worth reading is kept: an approval or a
	// request for changes, or any review with something to say.
	for _, r := range node.Reviews.Nodes {
		if r.Body == "" && r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" {
			continue
		}
		detail.Timeline = append(detail.Timeline, PRComment{Author: r.Author.name(), Body: r.Body, Review: r.State, At: r.SubmittedAt})
	}
	slices.SortStableFunc(detail.Timeline, func(a, b PRComment) int { return a.At.Compare(b.At) })

	for _, t := range node.ReviewThreads.Nodes {
		thread := ReviewThread{Path: t.Path, Line: t.Line, Side: t.DiffSide, Resolved: t.IsResolved, Outdated: t.IsOutdated}
		for _, c := range t.Comments.Nodes {
			thread.Comments = append(thread.Comments, c.comment())
		}
		detail.Threads = append(detail.Threads, thread)
	}

	if len(node.Head.Nodes) > 0 {
		if rollup := node.Head.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
			detail.Checks = rollupState(rollup.State)
			for _, c := range rollup.Contexts.Nodes {
				run := CheckRun{Name: c.Name, Verdict: c.Conclusion, URL: c.DetailsURL}
				if c.Typename == "StatusContext" {
					run = CheckRun{Name: c.Context, Verdict: c.State, URL: c.TargetURL}
				} else if run.Verdict == "" {
					run.Verdict = c.Status
				}
				run.State = verdictState(run.Verdict)
				detail.CheckRuns = append(detail.CheckRuns, run)
			}
		}
	}
	return detail
}

func rollupState(state string) ChecksState {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return ChecksPassing
	case "FAILURE", "ERROR":
		return ChecksFailing
	}
	return ChecksPending
}

// verdictState reads one check's own word. A skipped or neutral check passed in the sense that
// matters: it is not what is holding the pull request up.
func verdictState(verdict string) ChecksState {
	switch strings.ToUpper(verdict) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPassing
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ERROR", "CANCELLED", "ACTION_REQUIRED":
		return ChecksFailing
	}
	return ChecksPending
}

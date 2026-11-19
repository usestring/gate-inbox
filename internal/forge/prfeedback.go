package forge

import "time"

type PRFeedbackSnapshot struct {
	HeadSHA, BaseSHA, Mergeability   string
	ReviewsComplete, ThreadsComplete bool
	Reviews                          []PRReview
	Threads                          []PRThread
}

type PRReview struct {
	ID, HeadSHA, State, Author, AuthorType string
	SubmittedAt                            time.Time
}

type PRThread struct {
	ID, HeadSHA, AuthorType string
	Resolved, Outdated      bool
}

const prFeedbackFields = `
	baseRefOid
	reviews(first: 100) { pageInfo { hasNextPage } nodes {
		id state submittedAt author { __typename login } commit { oid }
	} }
	reviewThreads(first: 100) { pageInfo { hasNextPage } nodes {
		id isResolved isOutdated comments(first: 1) { nodes {
			author { __typename } originalCommit { oid }
		} }
	} }
`

type prFeedbackNode struct {
	BaseRefOID string `json:"baseRefOid"`
	Reviews    *struct {
		PageInfo *struct{ HasNextPage bool } `json:"pageInfo"`
		Nodes    []struct {
			ID, State   string
			SubmittedAt time.Time
			Author      struct {
				Type  string `json:"__typename"`
				Login string
			}
			Commit struct{ OID string }
		}
	} `json:"reviews"`
	ReviewThreads *struct {
		PageInfo *struct{ HasNextPage bool } `json:"pageInfo"`
		Nodes    []struct {
			ID                     string
			IsResolved, IsOutdated bool
			Comments               struct {
				Nodes []struct {
					Author struct {
						Type string `json:"__typename"`
					}
					OriginalCommit struct{ OID string }
				}
			}
		}
	} `json:"reviewThreads"`
}

func feedbackFrom(node prNode) *PRFeedbackSnapshot {
	if node.HeadRefOID == "" {
		return nil
	}
	result := &PRFeedbackSnapshot{HeadSHA: node.HeadRefOID, BaseSHA: node.BaseRefOID, Mergeability: node.Mergeable}
	if node.Reviews != nil {
		result.ReviewsComplete = node.Reviews.PageInfo != nil && !node.Reviews.PageInfo.HasNextPage && node.Reviews.Nodes != nil
		for _, r := range node.Reviews.Nodes {
			if r.ID == "" || r.Author.Type == "" || r.Author.Login == "" || (r.State != "PENDING" && r.SubmittedAt.IsZero()) {
				result.ReviewsComplete = false
			}
			result.Reviews = append(result.Reviews, PRReview{ID: r.ID, HeadSHA: r.Commit.OID,
				State: r.State, Author: r.Author.Login, AuthorType: r.Author.Type, SubmittedAt: r.SubmittedAt})
		}
	}
	if node.ReviewThreads != nil {
		result.ThreadsComplete = node.ReviewThreads.PageInfo != nil && !node.ReviewThreads.PageInfo.HasNextPage && node.ReviewThreads.Nodes != nil
		for _, t := range node.ReviewThreads.Nodes {
			if t.ID == "" || len(t.Comments.Nodes) != 1 {
				result.ThreadsComplete = false
				continue
			}
			root := t.Comments.Nodes[0]
			if root.Author.Type == "" || root.OriginalCommit.OID == "" {
				result.ThreadsComplete = false
			}
			result.Threads = append(result.Threads, PRThread{ID: t.ID, HeadSHA: root.OriginalCommit.OID,
				AuthorType: root.Author.Type, Resolved: t.IsResolved, Outdated: t.IsOutdated})
		}
	}
	return result
}

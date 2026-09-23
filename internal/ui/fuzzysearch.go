package ui

import (
	"strings"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func init() {
	algo.Init("default")
}

func fuzzyMetadataScore(sess store.Session, query string) (int, bool) {
	if strings.Contains(query, search.Wildcard) {
		return 0, false
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0, false
	}
	fields := []util.Chars{
		util.ToChars([]byte(sess.Name)),
		util.ToChars([]byte(sess.Tool)),
		util.ToChars([]byte(sess.Group)),
		util.ToChars([]byte(sess.Status)),
	}
	score := 0
	for _, term := range terms {
		best := -1
		pattern := []rune(term)
		for i := range fields {
			match, _ := algo.FuzzyMatchV2(false, false, true, &fields[i], pattern, false, nil)
			if match.Start >= 0 {
				best = max(best, match.Score)
			}
		}
		if best < 0 {
			return 0, false
		}
		score += best
	}
	return score, true
}

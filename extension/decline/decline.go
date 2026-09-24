// Package decline holds the one regexp that answers "did the worker decline
// the task", shared by every reader of a worker's own words: the handover
// filter that stubs a decline out of a replacement's context, and any
// extension that watches a worker's turns.
//
// The phrases are anchored to a first person subject and a refusal of the
// WORK -- "I can't help with", not "I can't reproduce". The distinction is
// the whole design: the first is a worker that will never act, the second is
// a finding. A false positive costs one stubbed line in a handover; a miss
// hands a replacement worker the refusal it was meant to be spared.
package decline

import "regexp"

// phrases are the openers a worker uses to decline the task itself.
var phrases = regexp.MustCompile(`(?i)\bI ?(?:can'?t|cannot|won'?t|'?m not able to|am not able to|'?m unable to|am unable to) ` +
	`(?:help (?:you )?with|assist (?:you )?with|comply|do that|proceed with|continue with|work on|create|build|develop|write|provide|run or improve|develop or validate)\b`)

// LooksLike reports whether the text declines the task. Anything it is
// unsure about it lets through, because whatever reads next -- a manager, a
// replacement worker -- can judge what a regexp cannot.
func LooksLike(text string) bool {
	return phrases.MatchString(text)
}

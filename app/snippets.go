package app

import "github.com/usestring/gate-inbox/internal/snippets"

// Snippet is one entry of snippets.json: a key and the text it types into the
// session in front of the operator and submits. Options.SnippetDefaults is a
// list of them.
type Snippet struct {
	// Key is the bare key: a single letter a-z, bound under ctrl+alt, or one
	// of the two keys that bind outside that chord, § and ±.
	Key string
	// Label is what the board calls the snippet. Empty falls back to Text.
	Label string
	// Text is what the key sends.
	Text string
}

// useSnippetDefaults hands the build's snippets to the loader. It is its own
// step so Run refuses an entry that could never bind before any face runs.
func useSnippetDefaults(entries []Snippet) error {
	converted := make([]snippets.Snippet, len(entries))
	for i, s := range entries {
		converted[i] = snippets.Snippet{Key: s.Key, Label: s.Label, Text: s.Text}
	}
	_, err := snippets.UseDistribution(converted)
	return err
}

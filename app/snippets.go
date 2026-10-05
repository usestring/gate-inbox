package app

import "github.com/usestring/gate-inbox/internal/snippets"

// Snippet is one entry of snippets.json: a key and the text it types into the
// session in front of the operator, and submits unless AutoSubmit is false. Options.SnippetDefaults is a
// list of them.
type Snippet struct {
	// Key is the menu key: a single letter a-z, or one of the two keys on
	// the physical key left of 1, § and ±.
	Key string
	// Label is what the board calls the snippet. Empty falls back to Text.
	Label string
	// Chord is the direct binding that sends it in one press without the
	// menu, e.g. "option+shift+r". Empty leaves it on the menu alone.
	Chord string
	// Text is what the key sends.
	Text string
	// AutoSubmit says whether the key presses Enter after Text. nil submits;
	// false leaves the text in the prompt.
	AutoSubmit *bool
}

// useSnippetDefaults hands the build's snippets to the loader. It is its own
// step so Run refuses an entry that could never bind before any face runs.
func useSnippetDefaults(entries []Snippet) error {
	converted := make([]snippets.Snippet, len(entries))
	for i, s := range entries {
		converted[i] = snippets.Snippet{Key: s.Key, Label: s.Label, Chord: s.Chord, Text: s.Text, AutoSubmit: s.AutoSubmit}
	}
	_, err := snippets.UseDistribution(converted)
	return err
}

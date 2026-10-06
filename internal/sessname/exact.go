package sessname

import "strings"

// ShortTitle reports whether title is already short enough to wear whole: at
// most maxWords words. A tool's own title that fits is used as it stands; a
// longer one is a sentence, and the session is named from its opening prompt
// instead.
func ShortTitle(title string) bool {
	words := len(tokenize(title))
	return words > 0 && words <= maxWords
}

// Exact is the compressor for a name that needs no squeezing: a short title,
// or one a model already picked. It kebab-cases the whole of it.
type Exact struct{}

// Compress returns the name kebab-cased, or nothing when no word survives.
func (Exact) Compress(name string) []string {
	kebab := strings.Join(tokenize(name), "-")
	if len(kebab) > maxLen {
		kebab = strings.TrimRight(kebab[:maxLen], "-")
	}
	if kebab == "" {
		return nil
	}
	return []string{kebab}
}

// Mixed is the compressor for a pass that names some rows from a title to wear
// whole and the rest from a sentence to squeeze: a title in Whole goes through
// Exact, everything else through Kebab.
type Mixed struct {
	Whole map[string]bool
}

// Compress picks Exact or Kebab for title.
func (m Mixed) Compress(title string) []string {
	if m.Whole[title] {
		return Exact{}.Compress(title)
	}
	return Kebab{}.Compress(title)
}

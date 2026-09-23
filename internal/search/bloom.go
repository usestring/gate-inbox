package search

// A session's text is scanned in blocks, and each block carries a bloom
// filter of the trigrams that start inside it. A query's own trigrams are
// looked up before the block is read, so a term that is not there costs a
// few dozen bit tests per block rather than a sweep of its bytes; only the
// blocks that may hold the term are scanned, and a false positive costs one
// scan, never a wrong answer.
//
// Sizing: a 256KB block holds up to 256K distinct trigram positions; 512K
// bits with one hash fill to about 40%, so a nine-character query (seven
// trigrams) falsely passes 0.4^7 of the blocks — a fraction of a percent.
// The filter is a quarter of the text's size.

const (
	// blockBytes is the text one filter covers, and the unit of scan work.
	blockBytes = 256 << 10
	// bloomTail is how far past its block a filter keeps reading, so a term
	// that starts inside the block and runs into the next is still found
	// through the block's own filter. A query longer than this is tested by
	// its first bloomTail bytes alone.
	bloomTail = 64
	bloomBits = 1 << 19
	// newlineStep is the spacing of the running turn count a hit's recency
	// is read from.
	newlineStep = 1 << 10
)

type bloom [bloomBits / 64]uint64

// trigramBit hashes three bytes to a bit of the filter.
func trigramBit(a, b, c byte) uint32 {
	h := (uint32(a)<<16 | uint32(b)<<8 | uint32(c)) * 0x9E3779B1
	return h >> (32 - 19)
}

func (f *bloom) set(bit uint32)      { f[bit/64] |= 1 << (bit % 64) }
func (f *bloom) has(bit uint32) bool { return f[bit/64]&(1<<(bit%64)) != 0 }

// mayHold is false only when the block certainly lacks the needle. A needle
// too short for a trigram cannot be filtered and passes.
func (f *bloom) mayHold(n needle) bool {
	for _, bit := range n.bits {
		if !f.has(bit) {
			return false
		}
	}
	return true
}

// mayHoldAll is the session-level test for a wildcard query: every segment
// must be possible in some block.
func (s *session) mayHoldAll(parts []needle) bool {
	for _, part := range parts {
		found := len(part.bits) == 0
		for i := range s.blooms {
			if s.blooms[i].mayHold(part) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// addTrigrams folds the trigrams starting at or after byte offset from into
// the filters. Filters only gain bits until a trim, so an append costs the
// new bytes alone: each trigram lands in its own block's filter and, when it
// starts within bloomTail of a block boundary, in the previous block's too.
func (s *session) addTrigrams(from int) {
	blocks := (len(s.text) + blockBytes - 1) / blockBytes
	if cap(s.blooms) < blocks {
		grown := make([]bloom, blocks)
		copy(grown, s.blooms)
		s.blooms = grown
	}
	s.blooms = s.blooms[:blocks]
	for p := max(0, from-2); p+3 <= len(s.text); p++ {
		bit := trigramBit(s.text[p], s.text[p+1], s.text[p+2])
		block := p / blockBytes
		s.blooms[block].set(bit)
		if block > 0 && p-block*blockBytes < bloomTail {
			s.blooms[block-1].set(bit)
		}
	}
	s.addNewlines(from)
}

// addNewlines extends the running turn count over text appended at from.
// Entry i counts the turns starting before byte i*newlineStep; the entries
// at or after from's segment are recomputed from the one before them.
func (s *session) addNewlines(from int) {
	segments := len(s.text)/newlineStep + 1
	if cap(s.newlines) < segments {
		grown := make([]int32, segments)
		copy(grown, s.newlines)
		s.newlines = grown
	}
	s.newlines = s.newlines[:segments]
	for i := max(1, from/newlineStep); i < segments; i++ {
		s.newlines[i] = s.newlines[i-1] + int32(turnStarts(s.text, (i-1)*newlineStep, i*newlineStep))
	}
}

// rebuildBlooms recomputes every filter, which a trim needs: bits cannot be
// taken back one by one.
func (s *session) rebuildBlooms() {
	// Clear in place: addTrigrams reslices the backing array and would
	// otherwise inherit the bits of the text that was just dropped.
	for i := range s.blooms {
		s.blooms[i] = bloom{}
	}
	s.blooms = s.blooms[:0]
	s.newlines = s.newlines[:0]
	s.addTrigrams(0)
}

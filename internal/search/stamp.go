package search

import (
	"hash/fnv"
	"io"
	"os"
	"time"
)

// A stamp is what an index pass compares to decide a file is unchanged since
// the last one, so it has to be able to tell two writes apart however close
// together they landed.
//
// Size and mtime alone cannot. A filesystem's mtime is only as fine as the
// clock behind it, and that clock is coarser than it looks: measured on this
// box, 56 of 400 consecutive writes to /dev/shm shared an mtime, against 0 of
// 400 on the disk. So a write that leaves the size alone -- a sqlite page
// rewritten in place, a record replaced -- can land inside one tick of that
// clock and read as a file nobody touched.
//
// That is why the tail is here. It is not read on every comparison: size or
// mtime differing already settles the question, and only when they agree does
// the tail get read to answer it. The cost is paid exactly where the cheap
// check would otherwise have been wrong.
type stamp struct {
	size  int64
	mtime int64
	// tail is a hash of the end of the file as of the last comparison that
	// needed one, and 0 when none has.
	tail uint64
	// tailRead records that tail was computed, since 0 is a hash a file can
	// legitimately have.
	tailRead bool
	// fresh marks a stamp taken so soon after the file's own mtime that a
	// write could have landed in the same tick and left the mtime alone.
	//
	// It is what keeps the tail off the common path. A file stamped a second
	// after it was last written cannot be hiding a write behind an identical
	// mtime -- any write since would have moved it -- so the stat settles it
	// and nothing is read. Only a stamp taken in the write's own tick has to
	// be checked against the bytes.
	fresh bool
}

// clockSlack is how close to a file's mtime a stamp has to be taken before it
// stops being trusted on its own. Comfortably wider than the tick that causes
// the collisions -- consecutive writes shared an mtime on tmpfs, so the tick is
// under a millisecond there -- and narrow enough that an idle file is never
// read.
const clockSlack = 50 * time.Millisecond

// tailBytes is how much of the end of a file the ambiguous case reads. Enough
// to catch an append or a rewritten record, small enough that reading it is far
// cheaper than the indexing pass it is deciding against.
const tailBytes = 64 << 10

// stampOfFile measures one file without reading it. A file that cannot be
// stat'd stamps as zero, which differs from any real file and so reads as
// changed -- the safe way round, since the pass that follows finds nothing to
// read and moves on.
func stampOfFile(path string) stamp {
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	mtime := info.ModTime().UnixNano()
	return stamp{
		size:  info.Size(),
		mtime: mtime,
		fresh: time.Since(time.Unix(0, mtime)) < clockSlack,
	}
}

// unchanged reports that nothing has been written to path since prev was taken,
// and returns the stamp to carry forward -- which is s plus whatever tail had
// to be read to reach the answer.
func (s stamp) unchanged(path string, prev stamp) (bool, stamp) {
	if s.size != prev.size || s.mtime != prev.mtime {
		return false, s
	}
	// Same size, same mtime. That is the answer unless the previous stamp was
	// taken close enough to the file's own mtime that a write could have
	// shared its tick, in which case the bytes are the tie-break.
	if !prev.fresh {
		s.tail, s.tailRead = prev.tail, prev.tailRead
		return true, s
	}
	s.tail, s.tailRead = tailOf(path, s.size)
	if !s.tailRead || !prev.tailRead {
		// Nothing to compare against yet, or the tail could not be read.
		// Calling it changed costs one indexing pass; calling it unchanged
		// could lose a write until something else touched the file.
		return false, s
	}
	return s.tail == prev.tail, s
}

// tailOf hashes the last tailBytes of a file. ok is false when the file could
// not be read, which leaves the caller to decide without it.
func tailOf(path string, size int64) (sum uint64, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	if _, err := file.Seek(max(size-tailBytes, 0), io.SeekStart); err != nil {
		return 0, false
	}
	digest := fnv.New64a()
	if _, err := io.Copy(digest, io.LimitReader(file, tailBytes)); err != nil {
		return 0, false
	}
	return digest.Sum64(), true
}

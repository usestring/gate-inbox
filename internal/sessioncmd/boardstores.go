package sessioncmd

import (
	"os"
	"sync"

	"github.com/usestring/gate-inbox/internal/store"
)

// maxIdleBoardStores bounds the handles kept between board reads. Extensions
// read from a few goroutines at once, and a burst past this many still gets
// a handle each; only the surplus is closed when it finishes.
const maxIdleBoardStores = 4

// boardStores are the store handles the board's reads have finished with.
//
// Board extensions read every session they watch on every pass, so a read
// that connects afresh pays for a new sqlite connection, with a cold page
// cache, once per session per pass. A read takes an idle handle instead and
// puts it back when done. Each read still holds a handle of its own for as
// long as it runs, exactly as when it opened one, so no read queues behind
// another on a store's single connection; see store.Reopen.
type boardStores struct {
	mu   sync.Mutex
	idle []boardStore
}

type boardStore struct {
	st *store.Store
	// file is the database the handle was opened on. A new file at the
	// same path is a different database, and a handle on the old one would
	// read rows nobody writes any more.
	file os.FileInfo
}

// openBoard is open for the board's reads, on a kept handle when there is
// one. The caller calls done instead of closing the store.
func (s *Sessions) openBoard() (opened *runtime, done func(), err error) {
	path := s.statePath()
	var file os.FileInfo
	opened, err = s.openOn(func() (*store.Store, error) {
		file, _ = os.Stat(path)
		if st := s.boardStores.take(file); st != nil {
			return st, nil
		}
		st, err := store.Open(path)
		if err == nil && file == nil {
			file, _ = os.Stat(path)
		}
		return st, err
	})
	if err != nil {
		return nil, nil, err
	}
	return opened, func() { s.boardStores.put(opened.store, file) }, nil
}

func (b *boardStores) take(file os.FileInfo) *store.Store {
	if file == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.idle) > 0 {
		last := b.idle[len(b.idle)-1]
		b.idle = b.idle[:len(b.idle)-1]
		if os.SameFile(last.file, file) {
			return last.st
		}
		last.st.Close()
	}
	return nil
}

func (b *boardStores) put(st *store.Store, file os.FileInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if file == nil || len(b.idle) >= maxIdleBoardStores {
		st.Close()
		return
	}
	b.idle = append(b.idle, boardStore{st: st, file: file})
}

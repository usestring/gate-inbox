package accounts

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
)

// ErrNoPool is smart routing with no account pool: no enabled extension
// supplies one, so there is nobody to borrow from and no quota
// to route on. Launching on the operator's own login, or on an account they
// name, never needs one.
var ErrNoPool = errors.New("account sharing is unavailable: no enabled extension supplies an account pool; set routing mode to own subscription in settings")

// poolTimeout bounds one question put to the pool. Each is asked while a
// launch waits on it.
const poolTimeout = 30 * time.Second

// borrowerTimeout is shorter: every launch asks who is borrowing, including
// one on the operator's own login that never uses the answer to route.
const borrowerTimeout = 10 * time.Second

var (
	poolMu      sync.Mutex
	resolvePool = func() (extension.AccountPool, error) { return nil, nil }
)

// UsePool sets how this process finds its account pool: resolve is called
// whenever sharing is needed, and returns nil when the build has none. It
// returns a func restoring the previous resolver, for tests.
func UsePool(resolve func() (extension.AccountPool, error)) (restore func()) {
	poolMu.Lock()
	defer poolMu.Unlock()
	previous := resolvePool
	resolvePool = resolve
	return func() {
		poolMu.Lock()
		defer poolMu.Unlock()
		resolvePool = previous
	}
}

// PoolAvailable reports whether an enabled extension supplies an account
// pool, so smart routing is worth offering.
func PoolAvailable() bool {
	_, err := currentPool()
	return err == nil
}

func currentPool() (extension.AccountPool, error) {
	poolMu.Lock()
	resolve := resolvePool
	poolMu.Unlock()
	pool, err := resolve()
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, ErrNoPool
	}
	return pool, nil
}

// activeBorrower is the operator this machine's launches are charged to.
func activeBorrower() (string, error) {
	pool, err := currentPool()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), borrowerTimeout)
	defer cancel()
	name, err := pool.Borrower(ctx)
	if err != nil {
		return "", err
	}
	if name = Normalize(name); name == "" {
		return "", errors.New("the account pool names no borrower")
	}
	return name, nil
}

// ownedBy is the pool's answer. With no pool, an account belongs only to
// the login of the same name.
func ownedBy(account, login string) bool {
	account, login = Normalize(account), Normalize(login)
	if login == "" {
		return false
	}
	pool, err := currentPool()
	if err != nil {
		return account == login
	}
	return pool.Owns(account, login)
}

func accountTool(tool config.Tool) extension.AccountTool {
	return extension.AccountTool{
		Env:    tool.AccountEnv,
		Secret: tool.AccountSecret,
		Listed: func() ([]string, error) { return List(tool) },
	}
}

func members(tool config.Tool) ([]string, error) {
	pool, err := currentPool()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), poolTimeout)
	defer cancel()
	return pool.Members(ctx, accountTool(tool))
}

func readUsage(tool config.Tool, account string) (Snapshot, error) {
	pool, err := currentPool()
	if err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), poolTimeout)
	defer cancel()
	usage, err := pool.Usage(ctx, accountTool(tool), account)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{At: time.Now(), ObservedAt: usage.ObservedAt, Windows: map[string]*Window{}}
	for name, window := range usage.Windows {
		snap.Windows[name] = &Window{Utilization: window.Utilization, ResetsAt: window.ResetsAt}
	}
	return snap, nil
}

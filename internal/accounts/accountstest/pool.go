// Package accountstest is an account pool a test controls, standing in for
// the one a distribution's extension supplies.
package accountstest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

// Pool answers as its fields say. The zero value names no borrower, pools
// every account the tool lists, and has no usage for any of them.
type Pool struct {
	// Login is the borrower. Empty means nobody is logged in.
	Login string
	// UsageOf answers Usage. Nil answers every account with an error.
	UsageOf func(account string) (extension.AccountUsage, error)

	mu    sync.Mutex
	reads int
}

// LoggedInAs is a pool whose borrower is login.
func LoggedInAs(login string) *Pool {
	return &Pool{Login: login}
}

// Resolve is the pool as accounts.UsePool takes it.
func (p *Pool) Resolve() (extension.AccountPool, error) {
	return p, nil
}

// Reads is how many times Usage has been asked.
func (p *Pool) Reads() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

func (p *Pool) Borrower(context.Context) (string, error) {
	if p.Login == "" {
		return "", errors.New("no login")
	}
	return p.Login, nil
}

// Owns holds an account to be the borrower's when it is the borrower's name
// with nothing or only digits after it: BOB owns BOB and BOB2, not BOBBY.
func (p *Pool) Owns(account, borrower string) bool {
	account, borrower = strings.ToUpper(account), strings.ToUpper(borrower)
	suffix, ok := strings.CutPrefix(account, borrower)
	if !ok || borrower == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (p *Pool) Members(_ context.Context, tool extension.AccountTool) ([]string, error) {
	return tool.Listed()
}

func (p *Pool) Usage(_ context.Context, _ extension.AccountTool, account string) (extension.AccountUsage, error) {
	p.mu.Lock()
	p.reads++
	p.mu.Unlock()
	if p.UsageOf == nil {
		return extension.AccountUsage{}, errors.New("no usage")
	}
	return p.UsageOf(account)
}

// Quota is a reading with the five-hour window used percent and resetting
// after reset, and the week used as much and resetting in a week.
func Quota(now time.Time, used float64, reset time.Duration) extension.AccountUsage {
	return extension.AccountUsage{Windows: map[string]extension.UsageWindow{
		"five_hour": {Utilization: &used, ResetsAt: now.Add(reset)},
		"seven_day": {Utilization: &used, ResetsAt: now.Add(7 * 24 * time.Hour)},
	}}
}

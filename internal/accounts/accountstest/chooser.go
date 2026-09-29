// Package accountstest is an account chooser a test controls, standing in
// for the one a build's extension supplies.
package accountstest

import (
	"context"
	"sync"

	"github.com/usestring/gate-inbox/extension"
)

// Chooser answers every request with Account, and records what it was
// asked.
type Chooser struct {
	Account string
	Err     error

	mu       sync.Mutex
	requests []extension.AccountRequest
}

// Resolve is the chooser as accounts.UseChooser takes it.
func (c *Chooser) Resolve() (extension.AccountChooser, error) {
	return c, nil
}

func (c *Chooser) ChooseAccount(_ context.Context, req extension.AccountRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return c.Account, c.Err
}

// Requests is everything the chooser was asked, in order.
func (c *Chooser) Requests() []extension.AccountRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]extension.AccountRequest(nil), c.requests...)
}

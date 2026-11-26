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

// Advisor is a Chooser that also advises on accounts, answering every
// AdviseAccounts with Advice (or Err) and recording what it was asked.
type Advisor struct {
	Chooser
	Advice    extension.Advice
	AdviceErr error

	adviceMu sync.Mutex
	asked    []extension.AdviceRequest
}

// Resolve is the advisor as accounts.UseChooser takes it.
func (a *Advisor) Resolve() (extension.AccountChooser, error) {
	return a, nil
}

func (a *Advisor) AdviseAccounts(_ context.Context, req extension.AdviceRequest) (extension.Advice, error) {
	a.adviceMu.Lock()
	defer a.adviceMu.Unlock()
	a.asked = append(a.asked, req)
	return a.Advice, a.AdviceErr
}

// Asked is every AdviseAccounts request, in order.
func (a *Advisor) Asked() []extension.AdviceRequest {
	a.adviceMu.Lock()
	defer a.adviceMu.Unlock()
	return append([]extension.AdviceRequest(nil), a.asked...)
}

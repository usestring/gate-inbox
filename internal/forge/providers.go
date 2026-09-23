package forge

// Providers is which built-in sources the operator has switched on.
type Providers struct {
	GitHub bool
	Linear bool
	// LinearAPIKey is the key Linear is read with. Linear switched on with no key is off: there
	// is nothing it could be asked, so it is unconfigured rather than failing.
	LinearAPIKey string
}

// NewResolvers builds a resolver for each source that is on, and nil for each that is off.
//
// A nil resolver is how the tracker knows a provider is off: it keeps no references of that kind,
// asks nothing and draws no rows for it. Building nothing, rather than a resolver that declines
// every question, is what makes "off" mean no background calls at all.
func NewResolvers(p Providers) (PRResolver, TicketResolver) {
	var prs PRResolver
	var tickets TicketResolver
	if p.GitHub {
		prs = NewGitHub()
	}
	if p.Linear && p.LinearAPIKey != "" {
		linear := NewLinear()
		linear.APIKey = p.LinearAPIKey
		tickets = linear
	}
	return prs, tickets
}

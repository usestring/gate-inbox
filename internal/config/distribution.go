package config

// Supplied is one setting the core leaves empty on purpose: an endpoint, a
// credential source or an identity that belongs to whoever runs the board,
// not to the board. A build for one estate -- a distribution -- fills these in
// for its operators; the core fills in none of them, so a board nobody
// configured sends nothing anywhere and reads no credential.
type Supplied struct {
	// Key is where the value lives: a dotted config.toml key, the name of
	// an environment variable when Env is set, or an interface in the
	// public extension package when Extension is set, which one of the
	// build's extensions implements.
	Key       string
	Env       bool
	Extension bool
	// Without says what the board does while the value is absent.
	Without string
}

// DistributionSupplied lists every such setting. It is the one place to
// read what a distribution has to bring, and the list the core's tests hold
// empty: a value that turns up in a default for one of these keys is a
// distribution's own default back in the core.
var DistributionSupplied = []Supplied{
	{Key: "tools.claude.account_env", Without: "sessions run on claude's own stored login; asking for a named account is refused"},
	{Key: "tools.claude.account_secret", Without: "no named account can be launched or listed"},
	{Key: "tools.claude.account_command", Without: "no named account can be launched"},
	{Key: "tools.claude.accounts_command", Without: "no accounts are listed"},
	{Key: "extensions.artifacts.base_url", Without: "the artifact tools stay off even when enabled"},
	{Key: "extensions.artifacts.key_secret", Without: "key_command gets an empty {secret}"},
	{Key: "extensions.artifacts.key_command", Without: "the artifact tools stay off even when enabled"},
	{Key: "extensions.artifacts.identity_command", Without: "artifacts are published with no name on them"},
	{Key: "AccountPoolProvider", Extension: true, Without: "smart routing is unavailable and refuses to launch; own-login and named-account launches work, charged to nobody"},
	{Key: "GATE_INBOX_TRACES", Env: true, Without: "nothing is traced"},
	{Key: "GATE_INBOX_TRACE_DATASET", Env: true, Without: "the axiom sink refuses to start"},
	{Key: "GATE_INBOX_TRACE_SECRET", Env: true, Without: "the axiom sink needs AXIOM_TRACE_TOKEN"},
	{Key: "GATE_INBOX_TRACE_PROJECT", Env: true, Without: "the axiom sink needs AXIOM_TRACE_TOKEN"},
}

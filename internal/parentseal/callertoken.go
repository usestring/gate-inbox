package parentseal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A caller token is what lets an MCP server that is not the session's own
// process believe a call is that session's: a server shared by many sessions
// learns its caller from the request, which any session could fill in with
// another's id. The token never enters a session's environment or its launch
// script, both of which a sandboxed agent can read: it goes only into files
// in the key dir, written by the launch for the session's own client to read
// (a claude's MCP config, an opencode session's binding). One key per config
// dir signs them all, kept beside the sealing keys so that a managed
// session's sandbox is denied it the same way; the dot keeps the name apart
// from the session ids that directory is keyed by.
//
// It holds against an agent naming another session, through a confused model
// or an injected prompt. It does not hold against a process of the same Unix
// user outside the sandbox, which can read the key; the parent-channel threat
// model says the same of the sealing keys.
const callerKeyFile = "caller.key"

// callerDomain is what every token signs besides its session. v1 tokens were
// exported into launch scripts, so none of them may verify now.
const callerDomain = "gate-inbox caller v2\n"

// agentSessionDir holds a binding per agent-CLI session id, for a client
// that names its own conversation on every request but cannot be made to
// send a header or argument: opencode's shared server sends
// _meta.sessionID on each tool call, and strips any argument a tool's
// schema does not advertise.
const agentSessionDir = "opencode-sessions"

// CallerToken is session's caller token, minting the key on first use.
func CallerToken(configDir, session string) (string, error) {
	key, err := mintKey(filepath.Join(KeyDir(configDir), callerKeyFile))
	if err != nil {
		return "", err
	}
	return callerToken(key, session)
}

// ExistingCallerToken is session's caller token, or empty while no key has
// been minted.
func ExistingCallerToken(configDir, session string) (string, error) {
	key, err := readKey(filepath.Join(KeyDir(configDir), callerKeyFile))
	if err != nil || key == nil {
		return "", err
	}
	return callerToken(key, session)
}

// VerifyCaller reports whether token is session's caller token. With no key
// minted no token was ever handed out, so none verifies.
func VerifyCaller(configDir, session, token string) bool {
	want, err := ExistingCallerToken(configDir, session)
	return err == nil && want != "" && hmac.Equal([]byte(want), []byte(token))
}

func callerToken(key []byte, session string) (string, error) {
	if !validSession.MatchString(session) {
		return "", fmt.Errorf("not a session id: %q", session)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(callerDomain + session))
	return hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

// BindAgentSession records that the agent CLI's own session agentSession is
// board session session, for AgentSessionCaller to resolve. The launch that
// learns the agent's id writes it; the binding carries session's token, so
// a file that did not come from a holder of the key binds nothing.
func BindAgentSession(configDir, agentSession, session string) error {
	path, err := agentSessionPath(configDir, agentSession)
	if err != nil {
		return err
	}
	token, err := CallerToken(configDir, session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(session + " " + token + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// AgentSessionCaller is the board session agentSession was bound to, or
// empty when it was bound to none, or its binding does not verify.
func AgentSessionCaller(configDir, agentSession string) string {
	path, err := agentSessionPath(configDir, agentSession)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	session, token, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	if !VerifyCaller(configDir, session, token) {
		return ""
	}
	return session
}

func agentSessionPath(configDir, agentSession string) (string, error) {
	if !validSession.MatchString(agentSession) {
		return "", fmt.Errorf("not an agent session id: %q", agentSession)
	}
	return filepath.Join(KeyDir(configDir), agentSessionDir, agentSession), nil
}

// serverDomain is what a shared server's proof signs besides the client's
// nonce; distinct from callerDomain, so a proof is never a caller token.
const serverDomain = "gate-inbox shared server v1\n"

// sessionServerDomain is what a shared server's proof to one session signs
// besides the nonce; distinct from serverDomain and callerDomain.
const sessionServerDomain = "gate-inbox shared server for a session v1\n"

// SessionServerProof is the proof, for nonce, that a server knows session
// token: keyed by the token, so a client that can read its own token but not
// the caller key -- a session confined away from the key dir
// (package keyconfine), left only its own MCP config -- can still check the
// server before handing it that token. Only a holder of the caller key can
// derive a session's token, so only the board's server can answer.
func SessionServerProof(token, nonce string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(sessionServerDomain + nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// ServerProofForSession is what configDir's server answers to session's
// client for nonce (SessionServerProof with the session's token). With no
// key minted there is no token, and so no proof.
func ServerProofForSession(configDir, session, nonce string) (string, error) {
	token, err := ExistingCallerToken(configDir, session)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("no caller key minted")
	}
	return SessionServerProof(token, nonce), nil
}

// ServerProof is the proof, for nonce, that a server holds configDir's
// caller key: what a launch checks before handing a session's token to
// whatever answers at the recorded address. Another user's process can take
// a loopback port; it cannot read the key.
func ServerProof(configDir, nonce string) (string, error) {
	key, err := mintKey(filepath.Join(KeyDir(configDir), callerKeyFile))
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(serverDomain + nonce))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

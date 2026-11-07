package parentseal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// A caller token is what lets an MCP server that is not the session's own
// process believe a call is that session's: a server shared by many sessions
// learns its caller from a request header or a tool argument, which any
// session could fill in with another's id. The launch hands each session its
// own token in its environment, so a session can prove who it is and nobody
// else's. One key per config dir signs them all, kept beside the sealing keys
// so that a managed session's sandbox is denied it the same way; the dot keeps
// the name apart from the session ids that directory is keyed by.
//
// It holds against an agent naming another session, through a confused model
// or an injected prompt. It does not hold against a process of the same Unix
// user outside the sandbox, which can read the key; the parent-channel threat
// model says the same of the sealing keys.
const callerKeyFile = "caller.key"

// CallerToken is session's caller token, minting the key on first use.
func CallerToken(configDir, session string) (string, error) {
	key, err := mintKey(filepath.Join(KeyDir(configDir), callerKeyFile))
	if err != nil {
		return "", err
	}
	return callerToken(key, session)
}

// ExistingCallerToken is session's caller token, or empty while no key has
// been minted: for a launch plan composed without side effects.
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
	mac.Write([]byte("gate-inbox caller v1\n" + session))
	return hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

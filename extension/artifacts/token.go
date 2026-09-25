package artifacts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// authHeader carries a signed publish or list request. Read links travel in
// the URL instead, because the URL is the thing being shared.
const authHeader = "X-Artifact-Auth"

// linkParam is the query parameter a read link's key travels in.
const linkParam = "k"

// authSkew is how far a signed request's timestamp may be from the worker's
// clock. It bounds replay without requiring the two to be in step; a
// laptop that has been asleep is the usual cause of a wider gap.
const authSkew = 5 * time.Minute

// claims is what a read link asserts. It is signed, not encrypted, so the
// holder can read it: that rules out putting the publishing account or
// session in here, since a link sent outside the team would carry them.
//
// The scope is one artifact. Without it a link to anything would open
// everything, which is the whole difference between a shared link and a
// shared password.
type claims struct {
	ID  string `json:"id"`
	Exp int64  `json:"exp"`
}

func b64(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeB64(encoded string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(encoded)
}

func mac(key, message []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(message)
	return h.Sum(nil)
}

// mintLink returns the key for one artifact, valid until exp.
//
// The signature covers the encoded payload rather than the claims struct,
// so the worker verifies the exact bytes it received and never has to
// re-encode JSON to check them. Field order, spacing and Go's map
// iteration therefore cannot break verification.
func mintLink(key []byte, id string, exp time.Time) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("no signing key")
	}
	if id == "" {
		return "", fmt.Errorf("no artifact id")
	}
	encoded, err := json.Marshal(claims{ID: id, Exp: exp.Unix()})
	if err != nil {
		return "", err
	}
	payload := b64(encoded)
	return payload + "." + b64(mac(key, []byte(payload))), nil
}

// verifyLink is the Go side of what the worker does per request. Nothing on
// the publish path needs it; it exists so the two implementations are
// checked against each other by test rather than by deployment.
//
// Every rejection is the same error. Telling a caller that a token was
// expired rather than forged tells an attacker which half to work on, and
// the holder of a bad link can do nothing with either answer but ask for
// another one.
func verifyLink(key []byte, token, id string, now time.Time) error {
	bad := fmt.Errorf("invalid or expired link")
	payload, signature, found := strings.Cut(token, ".")
	if !found {
		return bad
	}
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return bad
	}
	if !hmac.Equal(want, mac(key, []byte(payload))) {
		return bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return bad
	}
	var parsed claims
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return bad
	}
	if parsed.ID != id || parsed.Exp <= now.Unix() {
		return bad
	}
	return nil
}

// signRequest authenticates a publish or list call.
//
// The signature covers the method, the path, the timestamp and a digest of
// the body, so a captured header cannot be replayed against a different
// artifact, a different verb, or the same artifact with different content.
func signRequest(key []byte, method, path string, at time.Time, body []byte) string {
	digest := sha256.Sum256(body)
	stamp := strconv.FormatInt(at.Unix(), 10)
	canonical := strings.Join([]string{method, path, stamp, hex.EncodeToString(digest[:])}, "\n")
	return "v1:" + stamp + ":" + b64(mac(key, []byte(canonical)))
}

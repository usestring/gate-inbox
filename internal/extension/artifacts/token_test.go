package artifacts

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testKey = []byte("test-key-not-a-real-secret")
	testID  = "QUJDREVGR0hJSktMTU5PUA"
	testExp = time.Unix(1800000000, 0)
	testAt  = time.Unix(1700000000, 0)
)

// vectors is the contract between this package and the worker that
// verifies what it mints. Both sides read the same file, so a change to
// either implementation that breaks the other fails here or in
// worker/src/token.test.ts rather than on the next deploy.
type vectors struct {
	Key  string `json:"key"`
	Link struct {
		ID    string `json:"id"`
		Exp   int64  `json:"exp"`
		Token string `json:"token"`
	} `json:"link"`
	Request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Ts     int64  `json:"ts"`
		Body   string `json:"body"`
		Header string `json:"header"`
	} `json:"request"`
}

const vectorPath = "testdata/vectors.json"

// TestVectorsAreStable regenerates the shared fixture with UPDATE_VECTORS=1
// and otherwise checks this implementation still reproduces it byte for
// byte. A diff here means every link already handed out is about to stop
// verifying.
func TestVectorsAreStable(t *testing.T) {
	token, err := MintLink(testKey, testID, testExp)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	header := SignRequest(testKey, http.MethodPut, "/a/"+testID, testAt, []byte("hello"))

	var want vectors
	want.Key = string(testKey)
	want.Link.ID = testID
	want.Link.Exp = testExp.Unix()
	want.Link.Token = token
	want.Request.Method = http.MethodPut
	want.Request.Path = "/a/" + testID
	want.Request.Ts = testAt.Unix()
	want.Request.Body = "hello"
	want.Request.Header = header

	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	encoded = append(encoded, '\n')

	if os.Getenv("UPDATE_VECTORS") == "1" {
		if err := os.MkdirAll(filepath.Dir(vectorPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(vectorPath, encoded, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return
	}

	onDisk, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatalf("read %s (regenerate with UPDATE_VECTORS=1): %v", vectorPath, err)
	}
	if string(onDisk) != string(encoded) {
		t.Fatalf("token format changed; every link already issued would stop verifying.\non disk: %s\nnow:     %s", onDisk, encoded)
	}
}

func TestLinkRoundTrips(t *testing.T) {
	token, err := MintLink(testKey, testID, testExp)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := VerifyLink(testKey, token, testID, testExp.Add(-time.Hour)); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// A link scoped to one artifact must not open another. Without this check
// a link to anything is a link to everything.
func TestLinkIsScopedToOneArtifact(t *testing.T) {
	token, err := MintLink(testKey, testID, testExp)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := VerifyLink(testKey, token, "some-other-artifact", testExp.Add(-time.Hour)); err == nil {
		t.Fatal("a link for one artifact opened another")
	}
}

func TestLinkRejections(t *testing.T) {
	token, err := MintLink(testKey, testID, testExp)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	cases := map[string]struct {
		token string
		key   []byte
		now   time.Time
	}{
		"expired":        {token, testKey, testExp.Add(time.Second)},
		"wrong key":      {token, []byte("another-key"), testExp.Add(-time.Hour)},
		"no separator":   {strings.ReplaceAll(token, ".", ""), testKey, testExp.Add(-time.Hour)},
		"tampered claim": {"e30" + token[strings.Index(token, "."):], testKey, testExp.Add(-time.Hour)},
		"empty":          {"", testKey, testExp.Add(-time.Hour)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifyLink(tc.key, tc.token, testID, tc.now); err == nil {
				t.Fatal("accepted a link it should have refused")
			}
		})
	}
}

// Every rejection reads the same, so a caller cannot learn whether a link
// was forged or merely stale.
func TestRejectionsDoNotSayWhy(t *testing.T) {
	expired := VerifyLink(testKey, mustMint(t), testID, testExp.Add(time.Second))
	forged := VerifyLink([]byte("another-key"), mustMint(t), testID, testExp.Add(-time.Hour))
	if expired.Error() != forged.Error() {
		t.Fatalf("rejections differ: %q vs %q", expired, forged)
	}
}

// The signature has to cover the body, or a captured header republishes
// anything under the same id.
func TestRequestSignatureCoversBodyMethodAndPath(t *testing.T) {
	base := SignRequest(testKey, http.MethodPut, "/a/"+testID, testAt, []byte("hello"))
	cases := map[string]string{
		"different body":   SignRequest(testKey, http.MethodPut, "/a/"+testID, testAt, []byte("goodbye")),
		"different path":   SignRequest(testKey, http.MethodPut, "/a/other", testAt, []byte("hello")),
		"different method": SignRequest(testKey, http.MethodGet, "/a/"+testID, testAt, []byte("hello")),
		"different time":   SignRequest(testKey, http.MethodPut, "/a/"+testID, testAt.Add(time.Second), []byte("hello")),
	}
	for name, other := range cases {
		if other == base {
			t.Fatalf("%s produced the same signature", name)
		}
	}
}

func mustMint(t *testing.T) string {
	t.Helper()
	token, err := MintLink(testKey, testID, testExp)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return token
}

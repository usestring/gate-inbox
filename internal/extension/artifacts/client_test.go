package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	stubCommand(t, func(string) (string, error) { return string(testKey), nil })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := newClient(server.URL, newKeySource("read {secret}", "S"), time.Hour, 1024)
	client.now = func() time.Time { return testAt }
	return client, server
}

func TestPublishSignsTheRequestItSends(t *testing.T) {
	var seen *http.Request
	var body []byte
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))

	link, err := client.Publish(context.Background(), testID, []byte("<p>hi</p>"), Meta{Title: "Hi", ContentType: "text/html"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if seen.Method != http.MethodPut || seen.URL.Path != "/a/"+testID {
		t.Fatalf("sent %s %s", seen.Method, seen.URL.Path)
	}
	want := SignRequest(testKey, http.MethodPut, "/a/"+testID, testAt, body)
	if got := seen.Header.Get(AuthHeader); got != want {
		t.Fatalf("auth header %q, want %q", got, want)
	}
	if !strings.HasPrefix(link, "/") && !strings.Contains(link, "?"+LinkParam+"=") {
		t.Fatalf("link carries no key: %s", link)
	}
}

// The title is user text. Sent as a plain header it would corrupt the
// request the moment someone published "Q3 résumé" or a title with a
// newline in it.
func TestPublishCarriesMetadataThroughAnEncodedHeader(t *testing.T) {
	var header string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-Artifact-Meta")
		w.Write([]byte(`{}`))
	}))

	title := "Q3 résumé\nand a second line"
	if _, err := client.Publish(context.Background(), testID, []byte("x"), Meta{Title: title, ContentType: "text/plain"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	decoded, err := decodeMeta(header)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Title != title {
		t.Fatalf("title survived as %q", decoded.Title)
	}
}

func TestPublishRefusesAnOversizedArtifact(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("an oversized artifact reached the network")
	}))
	_, err := client.Publish(context.Background(), testID, make([]byte, 2048), Meta{ContentType: "text/plain"})
	if err == nil {
		t.Fatal("published something over the limit")
	}
}

func TestPublishReportsWhatTheStoreSaid(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not authorized to publish", http.StatusUnauthorized)
	}))
	_, err := client.Publish(context.Background(), testID, []byte("x"), Meta{ContentType: "text/plain"})
	if err == nil || !strings.Contains(err.Error(), "not authorized to publish") {
		t.Fatalf("error lost the cause: %v", err)
	}
}

func TestFetchTakesAShareLinkAsGiven(t *testing.T) {
	var seenQuery string
	client, server := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get(LinkParam)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>hi</p>"))
	}))

	token, err := MintLink(testKey, testID, testAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	link := server.URL + "/a/" + testID + "?" + LinkParam + "=" + token

	body, meta, err := client.Fetch(context.Background(), link)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if seenQuery != token {
		t.Fatalf("sent key %q, want the one in the link", seenQuery)
	}
	if string(body) != "<p>hi</p>" || meta.Bytes != 9 {
		t.Fatalf("got %q / %+v", body, meta)
	}
}

// A session that published something holds the key, so it can read it back
// from the bare id without being handed a link.
func TestFetchMintsAKeyForABareID(t *testing.T) {
	var seenQuery string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get(LinkParam)
		w.Write([]byte("body"))
	}))

	if _, _, err := client.Fetch(context.Background(), testID); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := VerifyLink(testKey, seenQuery, testID, testAt); err != nil {
		t.Fatalf("minted key does not verify: %v", err)
	}
}

func TestFetchRefusesNonsense(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, target := range []string{"", "   ", "https://example.test/nope/deep/path"} {
		if _, _, err := client.Fetch(context.Background(), target); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
}

func TestListSignsThePathWithoutItsQuery(t *testing.T) {
	var header, by string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get(AuthHeader)
		by = r.URL.Query().Get("by")
		json.NewEncoder(w).Encode(map[string]any{
			"artifacts": []Meta{{ID: "one", Title: "First", Email: "alice@example.test"}},
		})
	}))

	listed, err := client.List(context.Background(), 5, " alice@example.test ")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Email != "alice@example.test" {
		t.Fatalf("got %+v", listed)
	}
	if by != "alice@example.test" {
		t.Fatalf("filtered by %q", by)
	}
	// The filter narrows what comes back; it is not part of what is
	// authorized, so it stays out of the signature.
	if want := SignRequest(testKey, http.MethodGet, "/a", testAt, nil); header != want {
		t.Fatalf("signed %q, want %q", header, want)
	}
}

// Without a reachable key there is nothing to sign with, and the failure
// should name the secret rather than surface as a 401 from the store.
func TestCallsFailClearlyWithoutAKey(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return "", errors.New("no credentials") })
	client := newClient("https://example.test", newKeySource("read {secret}", "ARTIFACT_KEY"), time.Hour, 1024)
	_, err := client.Publish(context.Background(), testID, []byte("x"), Meta{ContentType: "text/plain"})
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_KEY") {
		t.Fatalf("error does not name the secret: %v", err)
	}
}

func TestLinkIsBuiltFromTheConfiguredBase(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return string(testKey), nil })
	client := newClient("https://artifacts.example.test/", newKeySource("read {secret}", "S"), time.Hour, 1024)
	client.now = func() time.Time { return testAt }
	link, err := client.Link(testID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Host != "artifacts.example.test" || parsed.Path != "/a/"+testID {
		t.Fatalf("link is %s", link)
	}
	if err := VerifyLink(testKey, parsed.Query().Get(LinkParam), testID, testAt); err != nil {
		t.Fatalf("link key does not verify: %v", err)
	}
}

// A truncated HTML document reads exactly like a whole one, so an artifact
// past the read limit is refused rather than shortened.
func TestFetchRefusesAnArtifactTooLargeToReadBack(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, readLimit+1))
	}))
	_, _, err := client.Fetch(context.Background(), testID)
	if err == nil {
		t.Fatal("returned a truncated artifact as if it were whole")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error does not say why: %v", err)
	}
}

func TestFetchAcceptsAnArtifactExactlyAtTheLimit(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, readLimit))
	}))
	body, _, err := client.Fetch(context.Background(), testID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(body) != readLimit {
		t.Fatalf("got %d bytes, want %d", len(body), readLimit)
	}
}

// The index key opens every artifact, so it must be scoped to the page and
// live a day rather than a month, and must not open any single artifact.
func TestIndexLinkIsScopedToThePageAndShortLived(t *testing.T) {
	stubCommand(t, func(string) (string, error) { return string(testKey), nil })
	client := newClient("https://artifacts.example.test", newKeySource("read {secret}", "S"), 30*24*time.Hour, 1024)
	client.now = func() time.Time { return testAt }

	link, err := client.IndexLink()
	if err != nil {
		t.Fatalf("index link: %v", err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Path != "/" {
		t.Fatalf("index link points at %q", parsed.Path)
	}
	key := parsed.Query().Get(LinkParam)
	if err := VerifyLink(testKey, key, indexScope, testAt.Add(23*time.Hour)); err != nil {
		t.Fatalf("index key does not open the index: %v", err)
	}
	if err := VerifyLink(testKey, key, indexScope, testAt.Add(25*time.Hour)); err == nil {
		t.Fatal("index key outlived a day")
	}
	if err := VerifyLink(testKey, key, testID, testAt); err == nil {
		t.Fatal("index key opened an artifact")
	}
}

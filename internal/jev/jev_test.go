package jev

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestEndpointRequiresAnHTTPSWorkerOrigin(t *testing.T) {
	originWithUser := url.URL{Scheme: "https", Host: "worker.example", User: url.User("fixture")}
	for url, want := range map[string]string{
		"":                              "",
		"http://worker.example":         "",
		originWithUser.String():         "",
		"https://worker.example/models": "",
		"https://worker.example/?key=x": "",
		"https://worker.example/#other": "",
		" https://worker.example/ ":     "https://worker.example/models",
	} {
		t.Setenv(URLEnv, url)
		if got := Endpoint(); got != want {
			t.Errorf("Endpoint() for %q = %q, want %q", url, got, want)
		}
	}
}

func TestAskNoulDoesNotFollowWorkerRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Worker request followed a redirect")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	if _, ok := AskNoul(t.Context(), HTTPClient, server.URL, "fake-key", nil, map[string]string{"done": "Is the work done?"}); ok {
		t.Fatal("a Worker redirect was accepted as a classifier result")
	}
}

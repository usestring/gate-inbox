package classifier

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderContracts(t *testing.T) {
	for _, provider := range []Provider{JEV, Clef} {
		t.Run(provider.Name(), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: %s %v", r.Method, r.Header)
				}
				var input request
				if provider == Clef {
					var envelope struct {
						Model string  `json:"model"`
						Input request `json:"input"`
					}
					if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
						t.Error(err)
					}
					input = envelope.Input
					if envelope.Model != WorkerModel || input.Model != ClefModel || r.Header.Get("X-Authorization") != "fixture-key" || r.Header.Get("Authorization") != "" {
						t.Errorf("incorrect Clef model or credentials: %+v", envelope)
					}
				} else {
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if input.Model != JEVModel || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("X-Authorization") != "" {
						t.Errorf("incorrect JEV model or credentials: %+v", input)
					}
				}
				if input.State != "bounded fixture text" {
					t.Errorf("lost state: %#v", input.State)
				}
				score := 0.95
				answers := make(map[string]answer)
				for name, q := range input.Questions {
					if q.Instructions == "" || (q.Type == "choice" && q.Criteria["c1"] != "candidate") {
						t.Errorf("lost question: %+v", q)
					}
					answers[name] = answer{Type: q.Type, Noul: &score, Choice: "c1"}
				}
				reply := response{Model: input.Model, Answers: answers}
				if provider == Clef {
					_ = json.NewEncoder(w).Encode([]response{reply})
				} else {
					_ = json.NewEncoder(w).Encode(reply)
				}
			}))
			defer server.Close()
			answers, ok := provider.AskNoul(t.Context(), server.Client(), server.URL, "fixture-key", "bounded fixture text", map[string]string{"launch": "Did the user ask to launch?"})
			if !ok || answers["launch"] != 0.95 {
				t.Fatalf("yes/no answer: %v, %v", answers, ok)
			}
			if pick := provider.AskChoice(t.Context(), server.Client(), server.URL, "fixture-key", "bounded fixture text", "Choose one", map[string]string{"c1": "candidate"}); pick != "c1" {
				t.Fatalf("choice answer: %q", pick)
			}
		})
	}
}

func TestProviderRejectsIncompleteOrInvalidAnswers(t *testing.T) {
	for _, provider := range []Provider{JEV, Clef} {
		for _, payload := range []string{
			`{`,
			`{"model":"wrong","answers":{"launch":{"type":"noul","noul":1}}}`,
			`{"answers":{}}`,
			`{"answers":{"launch":{"type":"noul"}}}`,
			`{"answers":{"launch":{"type":"noul","noul":1.1}}}`,
			`{"answers":{"launch":{"type":"choice","choice":"unknown"}}}`,
		} {
			t.Run(provider.Name()+"/"+payload, func(t *testing.T) {
				model := JEVModel
				if provider == Clef {
					model = ClefModel
				}
				payload := strings.Replace(payload, `{"answers"`, `{"model":"`+model+`","answers"`, 1)
				if provider == Clef {
					payload = "[" + payload + "]"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, payload) }))
				defer server.Close()
				if _, ok := provider.AskNoul(t.Context(), server.Client(), server.URL, "fixture", "text", map[string]string{"launch": "Launch?"}); ok {
					t.Fatal("invalid response authorized a launch")
				}
				if pick := provider.AskChoice(t.Context(), server.Client(), server.URL, "fixture", "text", "Pick", map[string]string{"c1": "candidate"}); pick != "" {
					t.Fatalf("invalid choice accepted: %q", pick)
				}
			})
		}
	}
}

func TestProviderNeverFollowsRedirects(t *testing.T) {
	for _, provider := range []Provider{JEV, Clef} {
		t.Run(provider.Name(), func(t *testing.T) {
			leaked := false
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			_, ok := provider.AskNoul(t.Context(), server.Client(), server.URL, "fixture", "private text", map[string]string{"launch": "Launch?"})
			if ok || leaked {
				t.Fatal("redirect followed or authorized a launch")
			}
		})
	}
}

func TestWorkerOriginValidation(t *testing.T) {
	for origin, want := range map[string]string{
		"":                                 "",
		"http://worker.example":            "",
		"https://user@localhost":           "",
		"https://worker.example/models":    "",
		"https://worker.example/?secret=x": "",
		"https://worker.example/#other":    "",
		" https://worker.example/ ":        "https://worker.example/models",
	} {
		t.Setenv(WorkerURLEnv, origin)
		if got := Clef.Endpoint(); got != want {
			t.Errorf("origin %q: %q, want %q", origin, got, want)
		}
	}
}

func TestProviderCredentialsStaySeparate(t *testing.T) {
	dir := t.TempDir()
	for _, provider := range []Provider{JEV, Clef} {
		t.Setenv(provider.KeyEnv(), "")
		path := provider.KeyPath(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(provider.Name()+"-saved"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(JEV.KeyEnv(), "jev-env")
	if JEV.Key(dir) != "jev-env" || Clef.Key(dir) != "Clef-saved" {
		t.Fatal("JEV environment affected Clef key")
	}
	t.Setenv(Clef.KeyEnv(), "clef-env")
	t.Setenv(JEV.KeyEnv(), "")
	if JEV.Key(dir) != "JEV-saved" || Clef.Key(dir) != "clef-env" {
		t.Fatal("Clef environment affected JEV key")
	}
}

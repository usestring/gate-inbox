// Package jev sends Clef questions through a configured Workers AI endpoint.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

const (
	Model       = "clef"
	WorkerModel = "@cf/cloudflare/clef"
	KeyEnv      = "WORKERS_API_KEY"
	URLEnv      = "WORKERS_AI_URL"
)

// The pasted Worker key sits beside the channel keys so the same sandbox
// rules protect it (see parentseal/keys.go).
const keyFile = "workers-ai.key"

var HTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func KeyPath(configDir string) string {
	return filepath.Join(parentseal.KeyDir(configDir), keyFile)
}

// ReadSavedKey is the key pasted into Settings, or empty when there is none.
func ReadSavedKey(configDir string) (string, error) {
	if configDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(KeyPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// Key reads the environment first and falls back to the key saved in Settings.
func Key(configDir string) string {
	if key := strings.TrimSpace(os.Getenv(KeyEnv)); key != "" {
		return key
	}
	key, _ := ReadSavedKey(configDir)
	return key
}

// Endpoint turns the configured HTTPS Worker origin into its /models URL.
func Endpoint() string {
	u, err := url.Parse(strings.TrimSpace(os.Getenv(URLEnv)))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	u.Path = "/models"
	return u.String()
}

type question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type answer struct {
	Type   string   `json:"type"`
	Noul   *float64 `json:"noul"`
	Choice string   `json:"choice"`
}

// AskNoul returns every requested yes-no probability, or fails the whole call.
func AskNoul(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]string) (map[string]float64, bool) {
	if len(questions) == 0 || len(questions) > 64 {
		return nil, false
	}
	asked := make(map[string]question, len(questions))
	for name, instructions := range questions {
		asked[name] = question{Type: "noul", Instructions: instructions}
	}
	answers := ask(ctx, client, endpoint, key, state, asked)
	out := make(map[string]float64, len(questions))
	for name := range questions {
		answer := answers[name]
		if answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			return nil, false
		}
		out[name] = *answer.Noul
	}
	return out, true
}

// AskChoice returns the selected criterion key, or an empty string on failure.
func AskChoice(ctx context.Context, client *http.Client, endpoint, key string, state any, instructions string, criteria map[string]string) string {
	answers := ask(ctx, client, endpoint, key, state, map[string]question{
		"next": {Type: "choice", Instructions: instructions, Criteria: criteria},
	})
	pick := answers["next"]
	if _, ok := criteria[pick.Choice]; pick.Type == "choice" && ok {
		return pick.Choice
	}
	return ""
}

func ask(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]question) map[string]answer {
	if endpoint == "" || key == "" {
		return nil
	}
	type modelInput struct {
		Model     string              `json:"model"`
		State     any                 `json:"state"`
		Questions map[string]question `json:"questions"`
	}
	body, err := json.Marshal(struct {
		Model string     `json:"model"`
		Input modelInput `json:"input"`
	}{
		Model: WorkerModel,
		Input: modelInput{Model: Model, State: state, Questions: questions},
	})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Authorization", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var results []struct {
		Model   string            `json:"model"`
		Answers map[string]answer `json:"answers"`
	}
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || json.Unmarshal(data, &results) != nil ||
		len(results) != 1 || results[0].Model != Model {
		return nil
	}
	return results[0].Answers
}

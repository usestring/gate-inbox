// Package jev asks TypeSafe's JEV classifier yes-no questions. The board's
// experiments and the spawn gate share it, so both read the same key and
// speak to the same model.
package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

const (
	Endpoint = "https://api.typesafe.ai/v1/systemone"
	Model    = "jev-1.13.0"
	KeyEnv   = "TYPESAFE_API_KEY"
)

// keyFile is the pasted TypeSafe key. It sits beside the sealing keys so
// managed sessions are denied it the same way (see parentseal/keys.go); the
// dot keeps the name apart from the session ids that directory is keyed by.
const keyFile = "typesafe.key"

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

// Key is the key a request uses: TYPESAFE_API_KEY wins over a saved key, the
// way every other credential here reads the environment first.
func Key(configDir string) string {
	if key := strings.TrimSpace(os.Getenv(KeyEnv)); key != "" {
		return key
	}
	key, _ := ReadSavedKey(configDir)
	return key
}

// AskNoul posts yes-no questions in one call against one state and returns
// each one's probability. Any failed request, or any question left without a
// probability, fails the whole call.
func AskNoul(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]string) (map[string]float64, bool) {
	asked := make(map[string]any, len(questions))
	for name, instructions := range questions {
		asked[name] = map[string]any{"type": "noul", "instructions": instructions}
	}
	body, err := json.Marshal(struct {
		Model     string `json:"model"`
		State     any    `json:"state"`
		Questions any    `json:"questions"`
	}{Model: Model, State: state, Questions: asked})
	if err != nil {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var reply struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply) != nil || reply.Model != Model {
		return nil, false
	}
	out := make(map[string]float64, len(questions))
	for name := range questions {
		answer, ok := reply.Answers[name]
		if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			return nil, false
		}
		out[name] = *answer.Noul
	}
	return out, true
}

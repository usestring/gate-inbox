package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

var HTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

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

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]question `json:"questions"`
}

type response struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
}

func (p Provider) AskNoul(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]string) (map[string]float64, bool) {
	if len(questions) == 0 || len(questions) > 64 {
		return nil, false
	}
	asked := make(map[string]question, len(questions))
	for name, instructions := range questions {
		asked[name] = question{Type: "noul", Instructions: instructions}
	}
	answers := p.ask(ctx, client, endpoint, key, state, asked)
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

func (p Provider) AskChoice(ctx context.Context, client *http.Client, endpoint, key string, state any, instructions string, criteria map[string]string) string {
	answers := p.ask(ctx, client, endpoint, key, state, map[string]question{
		"next": {Type: "choice", Instructions: instructions, Criteria: criteria},
	})
	pick := answers["next"]
	if _, ok := criteria[pick.Choice]; pick.Type == "choice" && ok {
		return pick.Choice
	}
	return ""
}

func (p Provider) ask(ctx context.Context, client *http.Client, endpoint, key string, state any, questions map[string]question) map[string]answer {
	if endpoint == "" || key == "" {
		return nil
	}
	input := request{State: state, Questions: questions}
	var payload any
	switch p {
	case JEV:
		input.Model = JEVModel
		payload = input
	case Clef:
		input.Model = ClefModel
		payload = struct {
			Model string  `json:"model"`
			Input request `json:"input"`
		}{WorkerModel, input}
	default:
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	if p == Clef {
		req.Header.Set("X-Authorization", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// Copy even an injected client so neither credential can follow a redirect.
	transport := *client
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := transport.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return nil
	}
	var reply response
	if p == Clef {
		var results []response
		if json.Unmarshal(data, &results) != nil || len(results) != 1 {
			return nil
		}
		reply = results[0]
	} else if json.Unmarshal(data, &reply) != nil {
		return nil
	}
	if reply.Model != input.Model {
		return nil
	}
	return reply.Answers
}

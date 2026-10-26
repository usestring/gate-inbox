package codexq

import (
	"encoding/json"
	"maps"
	"strings"
	"time"
)

const questionReplyOpen = "<send_user_message_question_reply>"
const questionReplyClose = "</send_user_message_question_reply>"

func applyQuestionReply(text string, at time.Time, calls []Call, index map[string]int) {
	text = strings.TrimSpace(text)
	body, ok := strings.CutPrefix(text, questionReplyOpen)
	if !ok {
		return
	}
	body, ok = strings.CutSuffix(body, questionReplyClose)
	if !ok {
		return
	}
	var replies []struct {
		Answer, Question, QuestionItemID string
	}
	if json.Unmarshal([]byte(body), &replies) != nil || len(replies) == 0 {
		return
	}
	type resolved struct {
		call, question int
		answer         string
	}
	updates := make([]resolved, 0, len(replies))
	seen := map[[2]int]bool{}
	for _, reply := range replies {
		var tuple []json.RawMessage
		var tool, id string
		var question *int
		if json.Unmarshal([]byte(reply.QuestionItemID), &tuple) != nil || len(tuple) != 3 ||
			json.Unmarshal(tuple[0], &tool) != nil || tool != askToolAsync ||
			json.Unmarshal(tuple[1], &id) != nil ||
			json.Unmarshal(tuple[2], &question) != nil || question == nil || strings.TrimSpace(reply.Answer) == "" {
			return
		}
		i, ok := index[id]
		if !ok {
			return
		}
		call := &calls[i]
		if !call.Async || call.State != Outstanding || !at.After(call.AskedAt) ||
			*question < 0 || *question >= len(call.Questions) {
			return
		}
		q := call.Questions[*question]
		// The UI truncates the displayed question; the tuple identifies the full original ask.
		if reply.Question == "" || !strings.HasPrefix(q.Question, reply.Question) {
			return
		}
		key := [2]int{i, *question}
		if _, answered := call.Answers[q.ID]; answered || seen[key] {
			return
		}
		seen[key] = true
		updates = append(updates, resolved{i, *question, reply.Answer})
	}
	for _, update := range updates {
		call := &calls[update.call]
		// Incremental readers may still hold the previous result.
		call.Answers = maps.Clone(call.Answers)
		call.AnswerTimes = maps.Clone(call.AnswerTimes)
		if call.Answers == nil {
			call.Answers = map[string]Answer{}
			call.AnswerTimes = map[string]time.Time{}
		}
		id := call.Questions[update.question].ID
		call.Answers[id] = Answer{Labels: []string{update.answer}}
		call.AnswerTimes[id] = at
		if len(call.Answers) == len(call.Questions) {
			call.State, call.ResolvedAt = Answered, at
		}
	}
}

func approvalNeedsChoice(call Call, reply string) bool {
	for _, q := range call.Questions {
		words := strings.Fields(q.Question)
		approval := strings.EqualFold(strings.TrimSpace(q.Header), "Approval")
		if len(words) > 0 {
			approval = approval || strings.EqualFold(strings.TrimRight(words[0], ":—-?!"), "Approval")
		}
		if !approval {
			continue
		}
		for _, option := range q.Options {
			if strings.TrimSpace(reply) == strings.TrimSpace(option.Label) {
				return false
			}
		}
		return true
	}
	return false
}

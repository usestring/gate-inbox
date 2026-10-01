// Package parentseal marks a message Gate Inbox types into a session so the
// session's own hook can tell, from the store and not from the text, who sent
// it.
//
// Every message another agent sends is typed into the recipient's prompt, where
// the recipient reads it as one more user turn. Words can say who sent it, but
// the sender writes words too, and a sibling can quote a parent's message word
// for word after reading it off the parent's pane. So the seal is not a word
// the recipient is asked to believe. It is an HMAC over the delivered text,
// keyed by a secret Gate Inbox keeps per recipient in its store, and it is
// checked by Gate Inbox's own UserPromptSubmit hook in the recipient's harness,
// which reports its verdict as hook context the harness keeps apart from typed
// text.
//
// The key never leaves the store: it is never typed, printed, drawn in a pane
// or given to a model, so read_session and the transcripts have nothing of it
// to show. What the pane does show, the seal line, is bound to one message id,
// one recipient, one sender and the exact text above it, and the hook accepts
// each message id once, so a copied seal verifies nothing new.
package parentseal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"regexp"
	"strconv"
	"strings"
)

// Version names the seal format; a change to what the MAC covers bumps it.
const Version = "v1"

// linePrefix opens every seal line. Bodies are neutralised against it (see
// Neutralise), so only Gate Inbox's delivery path ever writes it.
const linePrefix = "[gate-inbox seal "

// attestPrefix opens the attestation block Gate Inbox writes beside a relayed
// approval. It is neutralised in bodies for the same reason.
const AttestPrefix = "[gate-inbox relay attestation "

var sealLine = regexp.MustCompile(`^\[gate-inbox seal v1 m=([0-9]+) s=([A-Z2-7]{32})\]$`)

// MAC is the seal token for one delivery: which message, to whom, from whom,
// and the text typed above the seal, with whitespace runs collapsed so a
// terminal's paste handling cannot break it.
func MAC(key []byte, messageID int64, recipient, sender, text string) string {
	mac := hmac.New(sha256.New, key)
	for _, part := range []string{"gate-inbox-seal-" + Version, strconv.FormatInt(messageID, 10), recipient, sender,
		normalise(text)} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return base32.StdEncoding.EncodeToString(mac.Sum(nil)[:20])
}

// Line is the seal line Gate Inbox appends as the final line of a delivery.
func Line(messageID int64, token string) string {
	return linePrefix + Version + " m=" + strconv.FormatInt(messageID, 10) + " s=" + token + "]"
}

// Seal appends the seal line for text to it.
func Seal(key []byte, messageID int64, recipient, sender, text string) string {
	return text + "\n" + Line(messageID, MAC(key, messageID, recipient, sender, text))
}

// Split reads a submitted prompt back into the text that was sealed and the
// seal's message id and token. Only the prompt's final non-blank line is
// read: a seal anywhere else is quoted text, not Gate Inbox's.
//
// Claude Code hands a hook a pasted prompt inside one <pasted_content id="…">
// block. A prompt that is that block and nothing else is read from inside it;
// anything typed around the block leaves the closing tag, not a seal, as the
// final line.
func Split(prompt string) (text string, messageID int64, token string, ok bool) {
	trimmed := unwrapPaste(strings.TrimSpace(prompt))
	cut := strings.LastIndexByte(trimmed, '\n')
	if cut < 0 {
		return "", 0, "", false
	}
	match := sealLine.FindStringSubmatch(strings.TrimSpace(trimmed[cut+1:]))
	if match == nil {
		return "", 0, "", false
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return "", 0, "", false
	}
	return trimmed[:cut], id, match[2], true
}

var pasteOpen = regexp.MustCompile(`^<pasted_content id="([^"<>]{1,64})">\r?\n`)

// unwrapPaste returns the inside of prompt when prompt is exactly one
// pasted_content block, and prompt itself otherwise.
func unwrapPaste(prompt string) string {
	open := pasteOpen.FindStringSubmatch(prompt)
	if open == nil {
		return prompt
	}
	closing := `</pasted_content id="` + open[1] + `">`
	if !strings.HasSuffix(prompt, closing) {
		return prompt
	}
	inner := prompt[len(open[0]) : len(prompt)-len(closing)]
	if strings.Contains(inner, closing) {
		return prompt
	}
	return strings.TrimRight(inner, " \t\r\n")
}

// Verify reports whether token is the seal for text.
func Verify(key []byte, messageID int64, recipient, sender, text, token string) bool {
	if len(key) == 0 {
		return false
	}
	return hmac.Equal([]byte(MAC(key, messageID, recipient, sender, text)), []byte(token))
}

// Neutralise defuses anything in a message body that imitates Gate Inbox's
// own marks, so a body can quote a seal or an attestation without it reading
// as one. The seal would not verify anyway (it is not the final line, and it
// is bound to another message's text); this keeps a model reading the pane
// from being misled by the look of it.
func Neutralise(body string) string {
	return imitation.ReplaceAllStringFunc(body, func(found string) string {
		return "[quoted, not verified: " + found[1:]
	})
}

var imitation = regexp.MustCompile(`(?i)\[\s*gate-inbox\s+(seal|relay\s+attestation)\b`)

func normalise(text string) string { return strings.Join(strings.Fields(text), " ") }

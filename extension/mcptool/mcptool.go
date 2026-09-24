// Package mcptool holds the small pieces every MCP tool the board serves is
// built from: the annotations a tool declares, a plain-text result, and the
// argument an agent may give inline or as a file. The host's own tools and an
// extension's are built the same way, so they answer alike.
package mcptool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Annotations is the hint set a tool declares. A read-only tool is also
// idempotent: calling it twice changes nothing either time.
func Annotations(readOnly, destructive, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    readOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  readOnly,
		OpenWorldHint:   &openWorld,
	}
}

// Text is a successful result carrying one message.
func Text(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

// TextArg resolves a tool argument an agent may give inline or as the path
// of a file holding it. A spawn brief runs to pages,
// and pasting that into a tool call means the whole text travels through
// the agent's context twice, once written to disk and once again as the
// call; the file form lets the agent write it once and name it. The server
// runs unsandboxed on the user's machine, so it reads the path itself.
//
// Exactly one of the two is taken. A relative path is refused rather than
// resolved, because the server's working directory is not the agent's and
// a brief read from the wrong checkout is worse than a refusal. A leading
// ~ is the caller's home, and trailing newlines are dropped so a file
// ending in one does not submit an empty turn after the text.
func TextArg(inline, path, inlineName, pathName string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return inline, nil
	}
	if strings.TrimSpace(inline) != "" {
		return "", fmt.Errorf("pass %s or %s, not both", inlineName, pathName)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s: %w", pathName, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", pathName, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", pathName, err)
	}
	text := strings.TrimRight(string(data), "\r\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s: %s is empty", pathName, path)
	}
	return text, nil
}

// RequiredTextArg is TextArg for a field the schema used to mark required.
// Offering the file twin makes both optional to the schema, so the refusal
// an omitted field earned from the client moves here.
func RequiredTextArg(inline, path, inlineName, pathName string) (string, error) {
	text, err := TextArg(inline, path, inlineName, pathName)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("pass %s or %s", inlineName, pathName)
	}
	return text, nil
}

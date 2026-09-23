package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// textArg resolves a tool argument an agent may give inline or as the path
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
func textArg(inline, path, inlineName, pathName string) (string, error) {
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

// requiredTextArg is textArg for a field the schema used to mark required.
// Offering the file twin makes both optional to the schema, so the refusal
// an omitted field earned from the client moves here.
func requiredTextArg(inline, path, inlineName, pathName string) (string, error) {
	text, err := textArg(inline, path, inlineName, pathName)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("pass %s or %s", inlineName, pathName)
	}
	return text, nil
}

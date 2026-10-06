package agentsession

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// CodexTitles reads the thread names codex keeps in session_index.jsonl, by
// thread id, for the ids in want. The file is append-only and a rename
// appends a new line, so the last line for an id wins. A missing file is no
// titles rather than an error: codex writes it only once a thread is named.
func CodexTitles(want map[string]bool) (map[string]string, error) {
	root := codexRoot()
	if root == "" {
		return map[string]string{}, nil
	}
	return codexTitlesFrom(filepath.Join(filepath.Dir(root), "session_index.jsonl"), want)
}

func codexTitlesFrom(index string, want map[string]bool) (map[string]string, error) {
	out := map[string]string{}
	if len(want) == 0 {
		return out, nil
	}
	f, err := os.Open(index)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var rec struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(scanner.Bytes(), &rec) != nil || !want[rec.ID] {
			continue
		}
		out[rec.ID] = rec.ThreadName
	}
	return out, scanner.Err()
}

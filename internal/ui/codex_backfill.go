package ui

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

type codexBackfillPane struct {
	id, cwd, text, socket, pane string
	launchedAt                  time.Time
}

type codexBackfillCandidate struct {
	id, cwd, path string
	excerpts      []string
}

type codexBackfillCacheEntry struct {
	size     int64
	modified time.Time
	excerpts []string
}

type codexBackfillIndex struct {
	files map[string]codexBackfillCacheEntry
}

func (ix *codexBackfillIndex) candidates(root string, dirs map[string]bool) ([]codexBackfillCandidate, error) {
	dbs, err := filepath.Glob(filepath.Join(filepath.Dir(root), "state_*.sqlite"))
	if err != nil || len(dbs) == 0 {
		return nil, err
	}
	var dbPath string
	var version int
	for _, path := range dbs {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "state_"), ".sqlite")
		n, err := strconv.Atoi(name)
		if err == nil && (dbPath == "" || n > version) {
			dbPath, version = path, n
		}
	}
	if dbPath == "" {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, cwd, rollout_path FROM threads WHERE archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if ix.files == nil {
		ix.files = map[string]codexBackfillCacheEntry{}
	}
	live := map[string]bool{}
	var candidates []codexBackfillCandidate
	for rows.Next() {
		var id, cwd, path string
		if err := rows.Scan(&id, &cwd, &path); err != nil {
			return nil, err
		}
		parsed, parseErr := uuid.Parse(id)
		if !dirs[cwd] || parseErr != nil || parsed.String() != id || path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		live[path] = true
		entry, ok := ix.files[path]
		if !ok || entry.size != info.Size() || !entry.modified.Equal(info.ModTime()) {
			excerpts, err := agentsession.CodexExcerpts(path)
			if err != nil {
				continue
			}
			entry = codexBackfillCacheEntry{size: info.Size(), modified: info.ModTime(), excerpts: excerpts}
			ix.files[path] = entry
		}
		if len(entry.excerpts) > 0 {
			candidates = append(candidates, codexBackfillCandidate{id: id, cwd: cwd, path: path, excerpts: entry.excerpts})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for path := range ix.files {
		if !live[path] {
			delete(ix.files, path)
		}
	}
	return candidates, nil
}

func matchCodexBackfills(panes []codexBackfillPane, candidates []codexBackfillCandidate, claimed map[string]bool) map[string]codexBackfillCandidate {
	hits := map[string][]codexBackfillCandidate{}
	owners := map[string]int{}
	for _, pane := range panes {
		if pane.text == "" {
			continue
		}
		for _, candidate := range candidates {
			if pane.cwd != candidate.cwd {
				continue
			}
			matched := false
			for _, excerpt := range candidate.excerpts {
				if strings.Contains(pane.text, excerpt) {
					matched = true
					break
				}
			}
			if matched {
				hits[pane.id] = append(hits[pane.id], candidate)
				owners[candidate.id]++
			}
		}
	}
	result := map[string]codexBackfillCandidate{}
	for _, pane := range panes {
		if matches := hits[pane.id]; len(matches) == 1 && owners[matches[0].id] == 1 && !claimed[matches[0].id] {
			result[pane.id] = matches[0]
		}
	}
	return result
}

func bindCodexBackfills(stor *store.Store, panes []codexBackfillPane, candidates []codexBackfillCandidate, claimed map[string]bool) map[string]search.Target {
	out := map[string]search.Target{}
	matches := matchCodexBackfills(panes, candidates, claimed)
	for _, pane := range panes {
		match, ok := matches[pane.id]
		if !ok {
			continue
		}
		bound, err := stor.BindAdoptedCodex(pane.id, pane.socket, pane.pane, match.id, pane.launchedAt)
		if err != nil || !bound {
			continue
		}
		out[pane.id] = search.Target{Key: pane.id, Tool: search.ToolCodex, AgentID: match.id, Path: match.path}
	}
	return out
}

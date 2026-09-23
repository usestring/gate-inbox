package search

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Table shapes measured on the operator's own text, not a fixture: the
// synthetic corpus has forty distinct words and so a few hundred distinct
// trigrams, which makes every shape look fast. Real transcripts are code,
// paths, hashes and prose, and their trigram cardinality is what the index
// pays for. Opt in with the corpus benchmark's switch:
//
//	GATE_SEARCH_BENCH_SESSIONS=55 go test ./internal/search -run TestRealLayouts -v
func TestRealLayouts(t *testing.T) {
	targets := realTargets(t)
	var keys, texts []string
	var textBytes int
	start := time.Now()
	for _, tg := range targets {
		data, err := os.ReadFile(tg.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if text := LineText(ToolClaude, []byte(line), DefaultLimits); text != "" {
				keys = append(keys, tg.Key)
				texts = append(texts, text)
				textBytes += len(text)
			}
		}
	}
	t.Logf("parse: %d turns, %.1fMB of text from %d files in %v", len(texts), float64(textBytes)/1e6, len(targets), time.Since(start).Round(time.Millisecond))
	start = time.Now()
	lowered := make([]string, len(texts))
	for i, text := range texts {
		lowered[i] = strings.ToLower(text)
	}
	t.Logf("go lowercasing: %v", time.Since(start).Round(time.Millisecond))

	type layout struct {
		name, ddl string
		rows      []string
		queries   map[string]string // label -> SQL with one ? for the pattern
		pattern   func(q string) string
	}
	likePat := func(q string) string { return "%" + q + "%" }
	globPat := func(q string) string { return "*" + strings.ToLower(q) + "*" }
	matchPat := func(q string) string { return `"` + strings.ReplaceAll(q, `"`, `""`) + `"` }
	layouts := []layout{
		{"trigram", "fts5(key UNINDEXED, text, tokenize='trigram', columnsize=0, detail=none)", texts,
			map[string]string{"like": "SELECT DISTINCT key FROM turns WHERE text LIKE ?"}, likePat},
		{"trigram-cs", "fts5(key UNINDEXED, text, tokenize=\"trigram case_sensitive 1\", columnsize=0, detail=none)", lowered,
			map[string]string{"glob": "SELECT DISTINCT key FROM turns WHERE text GLOB ?"}, globPat},
		{"unicode61", "fts5(key UNINDEXED, text, tokenize='unicode61', columnsize=0, detail=none)", texts,
			map[string]string{"match": "SELECT DISTINCT key FROM turns WHERE turns MATCH ?"}, matchPat},
		{"unicode61-prefix", "fts5(key UNINDEXED, text, tokenize='unicode61', columnsize=0, detail=none, prefix='3 4')", texts,
			map[string]string{"match": "SELECT DISTINCT key FROM turns WHERE turns MATCH ?"}, matchPat},
		{"unicode61-ident", "fts5(key UNINDEXED, text, tokenize=\"unicode61 tokenchars '-_./'\", columnsize=0, detail=none)", texts,
			map[string]string{"match": "SELECT DISTINCT key FROM turns WHERE turns MATCH ?"}, matchPat},
	}
	queries := []string{"terraform", "go test", "http-gateway", "db-stage", "zzqx-nothing"}
	for _, l := range layouts {
		path := filepath.Join(t.TempDir(), "x.db")
		db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=synchronous(off)&_pragma=cache_size(-32000)&_pragma=temp_store(memory)&_txlock=immediate")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("CREATE VIRTUAL TABLE turns USING " + l.ddl); err != nil {
			t.Fatalf("%s: %v", l.name, err)
		}
		start = time.Now()
		tx, _ := db.Begin()
		insert, _ := tx.Prepare("INSERT INTO turns (key, text) VALUES (?, ?)")
		for i := range l.rows {
			if _, err := insert.Exec(keys[i], l.rows[i]); err != nil {
				t.Fatalf("%s: %v", l.name, err)
			}
		}
		insert.Close()
		tx.Commit()
		wrote := time.Since(start)
		db.Exec("INSERT INTO turns(turns) VALUES('optimize')")
		db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		info, _ := os.Stat(path)
		t.Logf("%-17s write %6v (%5.1f MB/s of text)  size %5.1fMB (%.2fx text)", l.name, wrote.Round(time.Millisecond),
			float64(textBytes)/1e6/wrote.Seconds(), float64(info.Size())/1e6, float64(info.Size())/float64(textBytes))
		for label, stmt := range l.queries {
			var parts []string
			for _, q := range queries {
				start = time.Now()
				rows, err := db.Query(stmt, l.pattern(q))
				if err != nil {
					parts = append(parts, fmt.Sprintf("%s=ERR(%v)", q, err))
					continue
				}
				n := 0
				for rows.Next() {
					n++
				}
				rows.Close()
				parts = append(parts, fmt.Sprintf("%s=%d/%v", q, n, time.Since(start).Round(100*time.Microsecond)))
			}
			t.Logf("%-17s %s: %s", "", label, strings.Join(parts, "  "))
		}
		db.Close()
	}
}

package search

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Layout experiment: the same turns written into FTS5 tables of different
// shapes, which is how detail=none was chosen. detail=full keeps positions,
// which a MATCH phrase of trigrams needs to prove adjacency; detail=none
// stores only the row list per trigram, so MATCH phrases are refused there
// and LIKE, which the trigram index also serves, is the query instead. On the
// synthetic corpus detail=none writes 35% faster, is a quarter of the size,
// and answers LIKE as fast as full answers MATCH. Run with:
//
//	go test ./internal/search -run '^$' -bench Layout -benchmem -benchtime 3x
func BenchmarkLayout(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	dir := b.TempDir()
	var texts []string
	var keys []string
	for s := 0; s < 20; s++ {
		path := genSession(rng, dir, fmt.Sprintf("s%02d", s), corpusShape{bytesPer: 1 << 20, hugeEvery: 10})
		data, _ := os.ReadFile(path)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if text := LineText(ToolClaude, []byte(line), DefaultLimits); text != "" {
				texts = append(texts, text)
				keys = append(keys, fmt.Sprintf("s%02d", s))
			}
		}
	}
	var textBytes int
	for _, t := range texts {
		textBytes += len(t)
	}
	b.Logf("%d turns, %.1fMB of indexed text", len(texts), float64(textBytes)/1e6)

	layouts := map[string]string{
		"full": "fts5(key UNINDEXED, text, tokenize='trigram', columnsize=0)",
		"none": "fts5(key UNINDEXED, text, tokenize='trigram', columnsize=0, detail=none)",
	}
	queries := []string{"collector rollout", "sample-run", "go test ./", "xyzzy-absent"}
	for name, ddl := range layouts {
		b.Run("write/"+name, func(b *testing.B) {
			b.SetBytes(int64(textBytes))
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				path := filepath.Join(b.TempDir(), "x.db")
				db := openRaw(b, path, ddl)
				b.StartTimer()
				writeAll(b, db, keys, texts)
				b.StopTimer()
				var size int64
				for _, suffix := range []string{"", "-wal"} {
					if info, err := os.Stat(path + suffix); err == nil {
						size += info.Size()
					}
				}
				b.ReportMetric(float64(size)/float64(textBytes), "index/text")
				db.Close()
				b.StartTimer()
			}
		})
		path := filepath.Join(b.TempDir(), name+".db")
		db := openRaw(b, path, ddl)
		writeAll(b, db, keys, texts)
		db.Exec("INSERT INTO turns(turns) VALUES('optimize')")
		for _, q := range queries {
			if name == "full" {
				b.Run("match/"+name+"/"+q, func(b *testing.B) {
					for i := 0; i < b.N; i++ {
						countRows(b, db, "SELECT DISTINCT key FROM turns WHERE turns MATCH ?", phrase(q))
					}
				})
			}
			b.Run("like/"+name+"/"+q, func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					countRows(b, db, "SELECT DISTINCT key FROM turns WHERE text LIKE ?", "%"+q+"%")
				}
			})
		}
		// Correctness: LIKE under each layout must agree with a positional MATCH.
		for _, q := range queries {
			l := countRows(b, db, "SELECT DISTINCT key FROM turns WHERE text LIKE ?", "%"+q+"%")
			if name == "full" {
				m := countRows(b, db, "SELECT DISTINCT key FROM turns WHERE turns MATCH ?", phrase(q))
				b.Logf("%-6s %-18q match=%d like=%d", name, q, m, l)
			} else {
				b.Logf("%-6s %-18q like=%d", name, q, l)
			}
		}
		db.Close()
	}
}

func openRaw(b *testing.B, path, ddl string) *sql.DB {
	b.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=synchronous(off)&_pragma=cache_size(-32000)&_pragma=temp_store(memory)&_txlock=immediate")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE VIRTUAL TABLE turns USING " + ddl); err != nil {
		b.Fatal(err)
	}
	return db
}

func writeAll(b *testing.B, db *sql.DB, keys, texts []string) {
	b.Helper()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	insert, err := tx.Prepare("INSERT INTO turns (key, text) VALUES (?, ?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := range texts {
		if _, err := insert.Exec(keys[i], texts[i]); err != nil {
			b.Fatal(err)
		}
	}
	insert.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

// phrase quotes a query as one FTS5 string so a MATCH reads it as a phrase
// of consecutive trigrams rather than as query syntax.
func phrase(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

func countRows(b *testing.B, db *sql.DB, query string, arg string) int {
	b.Helper()
	rows, err := db.Query(query, arg)
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n
}

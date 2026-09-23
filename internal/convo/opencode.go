package convo

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/opencode"

	_ "modernc.org/sqlite"
)

// opencodeQueryFor reads the top-level conversations opencode has touched
// recently.
//
// parent_id is null for a conversation somebody is sitting in front of; the
// rows with a parent are subagents opencode spawned, which have titles of
// their own but no pane. time_archived filters the ones it has closed.
func opencodeQueryFor(sessions string) string {
	return "SELECT id, directory, title, time_updated\n\tFROM " + sessions + `
	WHERE parent_id IS NULL AND time_archived IS NULL AND time_updated >= ?
	ORDER BY time_updated DESC
	LIMIT 200`
}

// opencodeConversations reads opencode's live database read-only.
//
// mode=ro is not a preference. The file belongs to a running program -- 450MB
// of it, with a WAL beside it -- and opening it any other way would let this
// process take a write lock on somebody else's session store. immutable=0 is
// implied and wanted: the WAL has to be read for the query to see the titles
// opencode wrote a moment ago.
func (ix *Index) opencodeConversations(cost *Cost) ([]Conversation, error) {
	if ix.opencode == "" {
		return nil, nil
	}
	if _, err := os.Stat(ix.opencode); err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+ix.opencode+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Only the v2 layout is read. Probing per pass (not once at startup)
	// follows an upgrade that lands mid-run.
	schema, err := opencode.Layout(db)
	if err != nil {
		return nil, err
	}
	if schema != opencode.SchemaV2 {
		return nil, nil
	}

	cutoff := time.Now().Add(-openCodeWindow).UnixMilli()
	rows, err := db.Query(opencodeQueryFor("session_v2"), cutoff)
	if err != nil {
		return nil, err
	}
	cost.OpenCodeQ++

	var out []Conversation
	for rows.Next() {
		var id, dir string
		var title sql.NullString
		var updated sql.NullInt64
		if err := rows.Scan(&id, &dir, &title, &updated); err != nil {
			rows.Close()
			return out, err
		}
		out = append(out, Conversation{
			Tool:      "opencode",
			ID:        id,
			Cwd:       dir,
			Title:     deplaceholder(title.String),
			UpdatedAt: time.UnixMilli(updated.Int64),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	cost.OpenCodeQ++
	return out, opencodeExcerpts(db, out, cutoff)
}

// deplaceholder drops the title opencode writes before its title model has
// run: "New session - <timestamp>". It is never empty, so without this the
// naming pass compresses the timestamp into a row name the moment a session
// opens, and the row wears it until the real title arrives -- if it ever
// does. An empty title names nothing: the row keeps the placeholder the
// manager gave it until the conversation has a real one.
func deplaceholder(title string) string {
	if title == "New session" || strings.HasPrefix(title, "New session - ") {
		return ""
	}
	return title
}

// openCodeWindow bounds the query to conversations that could still be in a
// pane. The table holds every session ever opened.
const openCodeWindow = 24 * time.Hour

// opencodePartsQueryFor reads recent assistant prose for the candidate sessions.
//
// opencode stores every message part as a JSON blob, and only the ones typed
// "text" were ever on screen. The table is small -- under nine thousand rows on
// this machine, against 1.4GB of Claude transcripts -- so one bounded query
// covers it where the other side needs an incremental file walk.
//
// The parts table moved in v2 while keeping the columns this query reads:
// part on v1, session_message on v2.
func opencodePartsQueryFor(parts string) string {
	data := "data"
	if parts == "session_message" {
		data = "json_set(data, '$.type', type)"
	}
	return "SELECT session_id, " + data + " FROM " + parts + `
	WHERE time_created >= ?
	ORDER BY time_created DESC
	LIMIT 400`
}

// opencodeExcerpts attaches recent assistant text to the conversations it
// belongs to, so an opencode pane can be matched on what it is showing rather
// than on its directory alone.
func opencodeExcerpts(db *sql.DB, convos []Conversation, cutoff int64) error {
	if len(convos) == 0 {
		return nil
	}
	wanted := make(map[string]int, len(convos))
	for i, convo := range convos {
		wanted[convo.ID] = i
	}
	rows, err := db.Query(opencodePartsQueryFor("session_message"), cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID, data string
		if err := rows.Scan(&sessionID, &data); err != nil {
			return err
		}
		at, ok := wanted[sessionID]
		if !ok {
			continue
		}
		for _, text := range excerptTexts(data) {
			text = Normalize(text)
			if text == "" || len(convos[at].Excerpts) >= excerptCount {
				continue
			}
			convos[at].Excerpts = append(convos[at].Excerpts, text)
		}
	}
	return rows.Err()
}

// excerptTexts pulls the on-screen prose out of one message blob. v2 stores
// a whole projected message per row: a bare text part, a user message
// ({type:user, text}), or an assistant message whose prose lives in
// content[] items ({type:text}).
// Anything else -- tool calls, reasoning, unknown envelopes -- carries no
// prose and contributes nothing.
func excerptTexts(data string) []string {
	var part struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &part) != nil {
		return nil
	}
	switch part.Type {
	case "text", "user":
		return []string{part.Text}
	case "assistant":
		var out []string
		for _, item := range part.Content {
			if item.Type == "text" {
				out = append(out, item.Text)
			}
		}
		return out
	default:
		return nil
	}
}

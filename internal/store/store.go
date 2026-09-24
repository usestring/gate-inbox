// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracing"
	_ "modernc.org/sqlite"
)

// ErrSessionGone reports a write against a session row that is no longer
// there. Deleting a session is normal, so a caller holding a session
// listed a moment earlier can tell that race apart from a real failure.
var ErrSessionGone = errors.New("session no longer exists")

var ErrGroupExists = errors.New("group already exists")

// Where a session's name came from. Only the first two may ever be replaced
// automatically: the other two are somebody having said what a row is called,
// and a manager that overwrites them is a manager nobody can name a row in.
const (
	// SourceDerived is a name the manager made up from what it could see --
	// the working directory's basename, for a pane it adopted.
	SourceDerived = "derived"
	// SourceTitle is a name compressed from the agent's own title for the
	// conversation. Replaceable in turn, because the title drifts.
	SourceTitle = "title"
	// SourceAgent is a name the session's own agent asked for.
	SourceAgent = "agent"
	// SourceUser is a name a person typed.
	SourceUser = "user"
)

// autoNamable is the sources an automatic rename may replace, written as a
// SQL fragment so the check happens inside the UPDATE rather than in a read
// the user can rename between.
const autoNamable = `name_source IN ('` + SourceDerived + `', '` + SourceTitle + `')`

type Session struct {
	ID       string
	Name     string
	Tool     string
	Cwd      string
	Group    string
	Status   string
	Archived bool
	// ArchivedAt is when the row was filed away, and the clock the retention
	// window runs on: a row still archived a week later is one nobody came
	// back for, and the sweep deletes it. Zero for a row that is not filed.
	ArchivedAt   time.Time
	Acked        bool
	CreatedAt    time.Time
	LastStatusAt time.Time
	// AgentSessionID is the agent CLI's own conversation id (claude session
	// UUID, codex rollout id, opencode session id).
	// Revive resumes this exact conversation instead of the cwd's most recent one.
	AgentSessionID string
	// AgentLaunchedAt is when the agent process now in the pane started, which
	// restart and revive move forward while CreatedAt keeps marking the row's birth.
	// Zero for sessions that never relaunched, whose launch is CreatedAt.
	AgentLaunchedAt time.Time
	// RetiredAgentSessionID is the conversation a restart left behind, kept so
	// id capture never binds the fresh run back to the context it dropped.
	RetiredAgentSessionID string
	// TmuxSocket and TmuxPaneID are set for a pane the manager adopted rather
	// than created: the tmux server the pane lives on and its pane id. Both
	// stay empty for a managed session, which the manager can always find
	// again as gi_<id> on its own socket, so the pair is what tells the two
	// apart across a restart.
	TmuxSocket          string
	TmuxPaneID          string
	PendingInputs       []string
	PendingInputClaimed bool
	ParentID            string
	// SpawnedBy is the session that called create_session for this row, at
	// whatever depth. ParentID is where the row is drawn, which is only the
	// same thing while the caller is a root; see spawner.go.
	SpawnedBy string
	// Role is "<extension id>/<role>" for a session an extension launched to
	// play a part of its own, and empty for every other. It is set when the
	// row is created and never changes.
	Role string
	// ReplacedBy is the session that took this one's seat through a
	// replacement, set in the same write that retires this one, and empty
	// for a session nothing replaced.
	ReplacedBy string
	// MigrationID joins migrated sessions; on creation it names the source session.
	MigrationID      string
	MigrationOpening []string
	// Model is the model this session was launched on, empty for the CLI's
	// own default. Kept so a revive, an unpark and a restart come back on the
	// same one rather than on whatever the default has become.
	Model string
	// Account is the named subscription this session was launched on, empty
	// for the CLI's own login. Kept for the same reason Model is: a revive
	// spends the same person's usage window, not whoever is logged in now.
	// It is a name, never the token.
	Account string
	// LaunchPrompt is the prompt handed to the agent on its command line.
	// Pending input waits for it to show in the pane, because an agent
	// taking it clears the composer and anything pasted there.
	LaunchPrompt string
	// NameSource is who chose Name, and is the only thing standing between
	// automatic naming and a name somebody typed. See the Source constants.
	NameSource string
	// Priority is how much this session's work matters: triage lifts it
	// within whichever queue it is in. A session inside a tiered group
	// inherits that tier unless it states a higher one of its own; see
	// EffectiveTier. It says nothing about status -- what the session is
	// doing and how much it matters are the two keys triage sorts on, in
	// that order.
	Priority priority.Tier

	// position is where ListSessions' query ordered the row; see ListKeys.
	position rowKey
}

// LaunchTime is when the agent now in the pane started: the last restart
// or revive, or the row's creation for a session that never relaunched.
func (sess Session) LaunchTime() time.Time {
	if sess.AgentLaunchedAt.IsZero() {
		return sess.CreatedAt
	}
	return sess.AgentLaunchedAt
}

type Store struct {
	db *sql.DB
	// path is the file db points at, kept so Reopen can reach it.
	path string
}

// busyTimeout is how long a writer waits for the lock before giving up.
// The manager and every `gate-inbox mcp` process share this database,
// so without it a collision between the poller's write and a session
// tool's write fails instantly with SQLITE_BUSY instead of waiting.
// _txlock=immediate takes the write lock at BEGIN, so a multi-statement
// transaction cannot fail halfway through trying to upgrade.
//
// synchronous=NORMAL is what keeps a commit off the event loop's critical
// path. At the FULL sqlite defaults to, every commit fsyncs the write-ahead
// log: on the disk this manager runs on that measured 215ms against 18µs at
// NORMAL, and the UI writes on a keypress.
//
// NORMAL is the setting WAL is designed around. It stays durable across a
// crash of any process here -- the log is still fsynced at checkpoints -- and
// what an OS crash can lose is which pane a row was last seen on, which the
// next poll recomputes two seconds later.
const busyTimeout = "?_pragma=busy_timeout(5000)&_pragma=synchronous(normal)&_txlock=immediate"

func Open(path string) (*Store, error) {
	store, err := connect(path)
	if err != nil {
		return nil, err
	}
	if err := store.init(); err != nil {
		store.db.Close()
		return nil, err
	}
	return store, nil
}

func connect(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+busyTimeout)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Reopen is a second handle to the same file, for work that must not queue
// behind this one. A store keeps a single connection, so a statement waiting
// on another process's write lock holds it for the whole wait and every other
// caller of that store waits with it -- including callers doing nothing but
// reading, which WAL would otherwise let through. Work that can afford to be
// late but must not make anything else late gets its own connection instead.
//
// The schema is already there, so this skips the migrations Open runs. The
// caller owns the handle and closes it.
func (s *Store) Reopen() (*Store, error) {
	return connect(s.path)
}

func (s *Store) Close() error {
	return s.db.Close()
}

// DeferCheckpoints stops this connection from checkpointing the write-ahead
// log when it commits, leaving that to whoever calls Checkpoint.
//
// sqlite checkpoints inline, on the connection that happened to commit once
// the log passes wal_autocheckpoint frames, and a checkpoint fsyncs the
// database. What that fsync costs is whatever the disk is busy with: under a
// millisecond idle, and 28 seconds measured here with the disk contended. It
// lands on one commit in every thousand, and because a store keeps a single
// connection, the commit that draws that card blocks every other caller with
// it -- including the keypress the operator is waiting on. Averages hide it
// completely: the other 999 commits cost 11us.
//
// Only a process that runs a checkpointer may call this. Every other one
// leaves the inline checkpoint alone, which is what keeps the log bounded
// when the manager is not running.
func (s *Store) DeferCheckpoints() error {
	_, err := s.db.Exec("PRAGMA wal_autocheckpoint=0")
	return err
}

// Checkpoint folds the write-ahead log back into the database.
//
// PASSIVE is the point: it copies what it can without waiting for readers or
// blocking a writer, so the fsync is paid here rather than by whichever
// commit would otherwise have tripped the threshold. A log it cannot fully
// drain this time is drained by the next call.
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	tool           TEXT NOT NULL,
	cwd            TEXT NOT NULL,
	group_name     TEXT NOT NULL,
	status         TEXT NOT NULL,
	archived       INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	last_status_at INTEGER NOT NULL,
	agent_session_id TEXT NOT NULL DEFAULT '',
	pending_inputs TEXT NOT NULL DEFAULT '[]',
	pending_claimed INTEGER NOT NULL DEFAULT 0,
	launch_prompt  TEXT NOT NULL DEFAULT '',
	model          TEXT NOT NULL DEFAULT '',
	account        TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS groups (
	name       TEXT PRIMARY KEY,
	sort_order INTEGER NOT NULL DEFAULT 0,
	path       TEXT NOT NULL DEFAULT '',
	archived   INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`)
	if err != nil {
		return err
	}
	// Migrate older databases that predate the group default-path column
	// and the session sort-order column.
	migrations := []string{
		`ALTER TABLE groups ADD COLUMN path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN acked INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE groups ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN snapshot TEXT NOT NULL DEFAULT ''`,
		// The three worktree columns are no longer read or written: the
		// manager stopped creating worktrees. They stay in the schema so an
		// older store opens unchanged and a downgraded binary finds them.
		`ALTER TABLE sessions ADD COLUMN worktree_repo TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN worktree_branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE groups ADD COLUMN worktree TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN agent_launched_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN retired_agent_session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN pending_inputs TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE sessions ADD COLUMN pending_claimed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`,
		// The session that actually called create_session, which parent_id
		// stopped being the moment a child spawned one. See spawner.go.
		`ALTER TABLE sessions ADD COLUMN spawned_by TEXT NOT NULL DEFAULT ''`,
		// Every existing row's best available answer, and the same one
		// spawnerColumn's fallback gives. Safe to re-run: a backfilled row is
		// no longer empty, and the one writer that clears spawned_by --
		// PlaceSession, on a release -- clears parent_id with it, so this
		// cannot put back what a release just gave up. The rows misfiled
		// before the column existed are backfilled to the root they were
		// hoisted to, because the session that really spawned them was never
		// recorded anywhere.
		`UPDATE sessions SET spawned_by = parent_id WHERE spawned_by = '' AND parent_id != ''`,
		`ALTER TABLE sessions ADD COLUMN launch_prompt TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN account TEXT NOT NULL DEFAULT ''`,
		// The parent's "(+N done)" outlives the rows it counts: the archive
		// retention sweep deletes a child seven days on, and a count derived
		// from the sessions table would shrink under the badge as it went.
		`CREATE TABLE IF NOT EXISTS child_archives (
			parent_id TEXT PRIMARY KEY,
			done      INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS session_inbox (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT    NOT NULL,
			sender_id    TEXT    NOT NULL,
			sender_name  TEXT    NOT NULL,
			body         TEXT    NOT NULL,
			fingerprint  TEXT    NOT NULL,
			sent_at      INTEGER NOT NULL,
			claimed_at   INTEGER NOT NULL DEFAULT 0,
			delivered_at INTEGER NOT NULL DEFAULT 0,
			read_at      INTEGER NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE session_inbox ADD COLUMN dropped_at INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS session_inbox_queue ON session_inbox (session_id, delivered_at, id)`,
		`CREATE INDEX IF NOT EXISTS session_inbox_sender ON session_inbox (session_id, sender_id, sent_at)`,
		`ALTER TABLE session_inbox ADD COLUMN subject TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_inbox ADD COLUMN superseded_by INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_inbox ADD COLUMN interrupt INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS session_inbox_subject ON session_inbox (session_id, sender_id, subject, delivered_at)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id               TEXT PRIMARY KEY,
			title            TEXT NOT NULL,
			body             TEXT NOT NULL DEFAULT '',
			owner_session_id TEXT NOT NULL DEFAULT '',
			state            TEXT NOT NULL DEFAULT 'pending',
			created_at       INTEGER NOT NULL DEFAULT 0,
			updated_at       INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS tasks_claimable ON tasks (state, created_at)`,
		`CREATE TABLE IF NOT EXISTS task_deps (
			task_id       TEXT NOT NULL,
			depends_on_id TEXT NOT NULL,
			PRIMARY KEY (task_id, depends_on_id)
		)`,
		`CREATE INDEX IF NOT EXISTS task_deps_reverse ON task_deps (depends_on_id)`,
		`CREATE TABLE IF NOT EXISTS file_reservations (
			id          TEXT    PRIMARY KEY,
			session_id  TEXT    NOT NULL,
			pattern     TEXT    NOT NULL,
			mode        TEXT    NOT NULL DEFAULT 'exclusive',
			note        TEXT    NOT NULL DEFAULT '',
			acquired_at INTEGER NOT NULL DEFAULT 0,
			expires_at  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS file_reservations_holder ON file_reservations (session_id, pattern)`,
		`CREATE INDEX IF NOT EXISTS file_reservations_live ON file_reservations (expires_at)`,
		`ALTER TABLE sessions ADD COLUMN tmux_socket TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN tmux_pane_id TEXT NOT NULL DEFAULT ''`,
		// Existing rows default to the one source automatic naming may never
		// touch. A database written before this column existed carries no
		// record of who named its rows, and guessing wrong in that direction
		// overwrites somebody's own name; guessing wrong in the other
		// direction only leaves a row looking the way it already looks.
		`ALTER TABLE sessions ADD COLUMN name_source TEXT NOT NULL DEFAULT 'user'`,
		// Rows archived before this column existed get a zero stamp, which
		// the sweep reads as "no clock started" and leaves alone. Backdating
		// them to the migration would delete somebody's archive on the first
		// poll after an upgrade; leaving them costs one manual pass.
		`ALTER TABLE sessions ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0`,
		// These two tables and the three columns added to the first below
		// belong to a feature this build no longer ships. They stay so a
		// database an older build wrote keeps opening; nothing reads them.
		`CREATE TABLE IF NOT EXISTS charters (
			session_id   TEXT PRIMARY KEY REFERENCES sessions(id),
			slug         TEXT NOT NULL,
			charter_path TEXT NOT NULL,
			ledger_path  TEXT NOT NULL,
			state        TEXT NOT NULL DEFAULT 'active',
			created_at   TEXT NOT NULL,
			compactions  INTEGER NOT NULL DEFAULT 0,
			ledger_hash  TEXT NOT NULL DEFAULT '',
			events_since_ledger_change INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS decisions (
			id           INTEGER PRIMARY KEY,
			session_id   TEXT NOT NULL,
			at           TEXT NOT NULL,
			event        TEXT NOT NULL,
			decision     TEXT NOT NULL,
			message      TEXT NOT NULL,
			rationale    TEXT NOT NULL,
			guard        TEXT NOT NULL DEFAULT '',
			model        TEXT NOT NULL DEFAULT '',
			cost_usd     REAL NOT NULL DEFAULT 0,
			delivered_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS decisions_session ON decisions (session_id, at)`,
		`ALTER TABLE charters ADD COLUMN charter_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE charters ADD COLUMN subsessions_total INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE charters ADD COLUMN armed_boundary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE groups ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`,
		// The tier that replaced those flags. A new column rather than a
		// widening of the old one because the flag's 0 means "unmarked"
		// and a tier's 0 would mean "urgent": reusing the column would
		// promote every untriaged session on the board at once.
		`ALTER TABLE sessions ADD COLUMN priority_tier TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE groups ADD COLUMN priority_tier TEXT NOT NULL DEFAULT ''`,
		// Marks made before tiers existed become High: the operator said
		// this one goes before the others, and High is that sentence on
		// the new scale. Urgent is not, because it now outranks rows the
		// mark used to tie with.
		//
		// Safe to re-run on every start because the writers keep the old
		// flag in step (see SetPriority): a tier cleared by hand clears
		// the flag with it, so this cannot resurrect what somebody just
		// cleared.
		`UPDATE sessions SET priority_tier = 'high' WHERE priority = 1 AND priority_tier = ''`,
		`UPDATE groups SET priority_tier = 'high' WHERE priority = 1 AND priority_tier = ''`,
		`CREATE TABLE IF NOT EXISTS session_prs (
			session_id TEXT NOT NULL,
			url        TEXT NOT NULL,
			first_seen INTEGER NOT NULL,
			PRIMARY KEY (session_id, url)
		)`,
		`CREATE TABLE IF NOT EXISTS work_seen (
			key     TEXT PRIMARY KEY,
			seen_at INTEGER NOT NULL
		)`,
		// Resolved pull request and ticket state, so a restart does not ask
		// about the whole board at once. Keyed by the reference rather than
		// by session, because two sessions routinely name the same pull
		// request. forgestate.go carries why storing this is safe.
		`CREATE TABLE IF NOT EXISTS forge_prs (
			key            TEXT PRIMARY KEY,
			repo           TEXT NOT NULL,
			number         INTEGER NOT NULL,
			title          TEXT NOT NULL DEFAULT '',
			state          TEXT NOT NULL DEFAULT '',
			checks         TEXT NOT NULL DEFAULT '',
			review         TEXT NOT NULL DEFAULT '',
			url            TEXT NOT NULL DEFAULT '',
			mergeable      INTEGER NOT NULL DEFAULT 1,
			head_ref       TEXT NOT NULL DEFAULT '',
			failing_checks INTEGER NOT NULL DEFAULT 0,
			fetched_at     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS forge_tickets (
			key        TEXT PRIMARY KEY,
			identifier TEXT NOT NULL,
			title      TEXT NOT NULL DEFAULT '',
			state      TEXT NOT NULL DEFAULT '',
			state_type TEXT NOT NULL DEFAULT '',
			assignee   TEXT NOT NULL DEFAULT '',
			url        TEXT NOT NULL DEFAULT '',
			fetched_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS forge_prs_age ON forge_prs (fetched_at)`,
		`CREATE INDEX IF NOT EXISTS forge_tickets_age ON forge_tickets (fetched_at)`,
		`ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT ''`,
		// A replacement an extension holds, recorded before its pane starts
		// so a board killed mid-hold leaves something the next start can
		// abort. See RecordHold.
		`CREATE TABLE IF NOT EXISTS replace_holds (
			fresh_id   TEXT PRIMARY KEY,
			old_id     TEXT NOT NULL,
			owner      TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`ALTER TABLE sessions ADD COLUMN replaced_by TEXT NOT NULL DEFAULT ''`,
	}
	for _, migration := range migrations {
		if _, err := s.db.Exec(migration); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	return s.BackfillNameSource()
}

// Retained for older binaries; current routing derives ownership from login.
const DefaultAccountSetting = "default_account"

// SetAccount records the account a session runs on from its next launch.
// The running process is untouched: a token is read at launch, so the row's
// account only takes effect through a restart, which is the caller's to do.
func (s *Store) SetAccount(id, account string) error {
	res, err := s.db.Exec(`UPDATE sessions SET account = ? WHERE id = ?`, account, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("session %s not found", id)
	}
	return nil
}

// DefaultAccount reads the board's default account, empty for none.
func (s *Store) DefaultAccount() (string, error) {
	return s.Setting(DefaultAccountSetting)
}

func (s *Store) Setting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) CreateSession(sess Session) error {
	return s.createSession(sess, "", false, nil)
}

// LaunchSession reserves the row id before exposing its pane to adoption.
// A failed launch rolls back the row and its fan-out charge together.
// The callback must not use this Store, whose connection holds the transaction.
func (s *Store) LaunchSession(sess Session, launch func() error) error {
	return s.createSession(sess, "", false, launch)
}

func (s *Store) LaunchSessionBeside(sess Session, anchorID string, launch func() error) error {
	return s.createSession(sess, anchorID, false, launch)
}

func (s *Store) LaunchSessionLeaf(sess Session, launch func() error) error {
	return s.createSession(sess, "", true, launch)
}

// CreateSessionBeside reads the anchor inside the write transaction, so a
// placement that lands first decides where the new row goes rather than
// leaving it behind.
func (s *Store) CreateSessionBeside(sess Session, anchorID string) error {
	return s.createSession(sess, anchorID, false, nil)
}

// CreateSessionLeaf files a row that nothing will ever nest under -- a
// terminal -- so it may hang off a session that is itself a child.
func (s *Store) CreateSessionLeaf(sess Session) error {
	return s.createSession(sess, "", true, nil)
}

// createSession is the one write all creation entry points share, so
// it is where the span goes: a row created beside an anchor and a row created
// at the end of a group cost the same transaction, and splitting them into
// three span names would only make the total harder to ask for.
func (s *Store) createSession(sess Session, anchorID string, leaf bool, launch func() error) error {
	if !tracing.Enabled() {
		return s.insertSession(sess, anchorID, leaf, launch)
	}
	start := time.Now()
	err := s.insertSession(sess, anchorID, leaf, launch)
	recordOp("store.CreateSession", start, err, true)
	return err
}

func (s *Store) insertSession(sess Session, anchorID string, leaf bool, launch func() error) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	if sess.LastStatusAt.IsZero() {
		sess.LastStatusAt = sess.CreatedAt
	}
	pendingInputs, err := encodePendingInputs(sess.PendingInputs)
	if err != nil {
		return err
	}
	sess.ParentID = strings.TrimSpace(sess.ParentID)
	sess.SpawnedBy = strings.TrimSpace(sess.SpawnedBy)
	anchorID = strings.TrimSpace(anchorID)
	if sess.MigrationID != "" {
		anchorID = sess.MigrationID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if anchorID != "" {
		var anchorGroup, anchorParent string
		err := tx.QueryRow(`SELECT group_name, parent_id FROM sessions WHERE id = ?`, anchorID).Scan(&anchorGroup, &anchorParent)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %s: %w", anchorID, err)
		}
		if err != nil {
			return err
		}
		sess.ParentID = anchorParent
		sess.Group = anchorGroup
	}
	if sess.ParentID != "" {
		parentGroup, err := validParent(tx, sess.ID, sess.ParentID, leaf)
		if err != nil {
			return err
		}
		sess.Group = parentGroup
	}
	// A caller that did not name a spawner gets the row it is filed under,
	// which is what every writer before this column meant by its parent: the
	// TUI's own new-session key, a revive, a migration. Only the MCP create
	// path knows better, and it says so.
	if sess.SpawnedBy == "" {
		sess.SpawnedBy = sess.ParentID
	}
	_, err = tx.Exec(
		`INSERT INTO sessions (id, name, tool, cwd, group_name, status, archived, created_at, last_status_at, agent_session_id, tmux_socket, tmux_pane_id, pending_inputs, parent_id, spawned_by, launch_prompt, model, account, name_source, priority_tier, role, sort_order)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         (SELECT COALESCE(MAX(sort_order)+1, 0) FROM sessions WHERE group_name = ? AND parent_id = ?))`,
		sess.ID, sess.Name, sess.Tool, sess.Cwd, sess.Group, sess.Status,
		boolToInt(sess.Archived), encodeTime(sess.CreatedAt), encodeTime(sess.LastStatusAt), sess.AgentSessionID,
		sess.TmuxSocket, sess.TmuxPaneID, pendingInputs, sess.ParentID, sess.SpawnedBy, sess.LaunchPrompt, sess.Model, sess.Account,
		nameSourceOr(sess.NameSource), string(sess.Priority), sess.Role,
		sess.Group, sess.ParentID,
	)
	if err != nil {
		return err
	}
	if sess.MigrationID != "" {
		var link string
		if err := tx.QueryRow(`SELECT `+migrationColumn+` FROM sessions WHERE id = ?`, anchorID).Scan(&link); err != nil {
			return err
		}
		if link == "" {
			link = anchorID
		}
		if len(sess.MigrationOpening) == 0 {
			var opening, prompt string
			if err := tx.QueryRow(`SELECT `+migrationOpeningColumn+`, launch_prompt FROM sessions WHERE id = ?`, anchorID).Scan(&opening, &prompt); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(opening), &sess.MigrationOpening); err != nil {
				return err
			}
			if len(sess.MigrationOpening) == 0 && prompt != "" {
				sess.MigrationOpening = []string{prompt}
			}
		}
		opening, err := json.Marshal(sess.MigrationOpening)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, migrationOpeningPrefix+sess.ID, string(opening)); err != nil {
			return err
		}
		for _, id := range []string{anchorID, sess.ID} {
			if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, migrationKeyPrefix+id, link); err != nil {
				return err
			}
		}
	}
	if sess.Group != "" {
		if _, err := tx.Exec(
			`INSERT INTO groups (name, sort_order)
			 VALUES (?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
			 ON CONFLICT(name) DO NOTHING`, sess.Group); err != nil {
			return err
		}
	}
	if launch != nil {
		if err := launch(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ensureGroup registers a group by name if it does not exist, leaving any
// existing default path untouched. The empty root is never stored.
func (s *Store) ensureGroup(name string) error {
	if name == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO groups (name, sort_order)
		 VALUES (?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
		 ON CONFLICT(name) DO NOTHING`, name)
	return err
}

// CreateGroup registers a group path like "backend/api/auth" with an
// optional default working directory, updating the path if it already exists.
func (s *Store) CreateGroup(name, path string) error {
	if name == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO groups (name, path, sort_order)
		 VALUES (?, ?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
		 ON CONFLICT(name) DO UPDATE SET path = excluded.path`, name, path)
	return err
}

func (s *Store) AddGroup(name, path string) error {
	if name == "" {
		return errors.New("group name cannot be empty")
	}
	res, err := s.db.Exec(
		`INSERT INTO groups (name, path, sort_order)
		 VALUES (?, ?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
		 ON CONFLICT(name) DO NOTHING`, name, path)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("group %q: %w", name, ErrGroupExists)
	}
	return nil
}

func (s *Store) ListSessions(includeArchived bool) ([]Session, error) {
	if !tracing.Enabled() {
		return s.listSessions(includeArchived)
	}
	start := time.Now()
	sessions, err := s.listSessions(includeArchived)
	// Whether the archived rows were asked for rides the span because it is
	// the difference between two very different reads: the active list skips
	// them, while the archived view decodes every filed row's launch prompt
	// and takes several times as long to do it. Without the flag those two
	// arrive as one operation whose duration looks unstable for no reason.
	recordOp("store.ListSessions", start, err, false,
		tracing.Attr{Key: "rows", Value: len(sessions)},
		tracing.Attr{Key: "archived", Value: includeArchived})
	return sessions, err
}

func (s *Store) listSessions(includeArchived bool) ([]Session, error) {
	query := `SELECT id, name, tool, cwd, group_name, status, archived, archived_at, acked, created_at, last_status_at, agent_session_id, tmux_socket, tmux_pane_id, agent_launched_at, retired_agent_session_id, pending_inputs, pending_claimed, parent_id, ` + spawnerColumnOf("sessions") + `, launch_prompt, model, account, name_source, priority_tier, role, replaced_by, ` + migrationColumn + `, ` + migrationOpeningColumn + `, sort_order, rowid
	          FROM sessions`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}
	query += ` ORDER BY group_name, sort_order, created_at, rowid`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var archived, acked, pendingClaimed int
		var tier string
		var created, lastStatus, agentLaunched, archivedAt int64
		var pendingInputs, migrationOpening string
		if err := rows.Scan(&sess.ID, &sess.Name, &sess.Tool, &sess.Cwd,
			&sess.Group, &sess.Status, &archived, &archivedAt, &acked, &created, &lastStatus,
			&sess.AgentSessionID,
			&sess.TmuxSocket, &sess.TmuxPaneID,
			&agentLaunched, &sess.RetiredAgentSessionID, &pendingInputs, &pendingClaimed, &sess.ParentID, &sess.SpawnedBy, &sess.LaunchPrompt, &sess.Model, &sess.Account, &sess.NameSource, &tier, &sess.Role, &sess.ReplacedBy, &sess.MigrationID, &migrationOpening, &sess.position.SortOrder, &sess.position.RowID); err != nil {
			return nil, err
		}
		sess.position.Group, sess.position.Created = sess.Group, created
		sess.Priority = priority.Tier(tier)
		if err := json.Unmarshal([]byte(migrationOpening), &sess.MigrationOpening); err != nil {
			return nil, fmt.Errorf("decode migration opening: %w", err)
		}
		if err := json.Unmarshal([]byte(pendingInputs), &sess.PendingInputs); err != nil {
			return nil, fmt.Errorf("decode pending inputs for session %s: %w", sess.ID, err)
		}
		sess.Archived = archived != 0
		sess.ArchivedAt = decodeTime(archivedAt)
		sess.Acked = acked != 0
		sess.PendingInputClaimed = pendingClaimed != 0
		sess.CreatedAt = decodeTime(created)
		sess.LastStatusAt = decodeTime(lastStatus)
		sess.AgentLaunchedAt = decodeTime(agentLaunched)
		sessions = append(sessions, sess)
	}
	return OrderLinkedSessions(sessions), rows.Err()
}

func (s *Store) Get(id string) (Session, error) {
	var sess Session
	var archived, acked, pendingClaimed int
	var tier string
	var created, lastStatus, agentLaunched, archivedAt int64
	var pendingInputs, migrationOpening string
	err := s.db.QueryRow(
		`SELECT id, name, tool, cwd, group_name, status, archived, archived_at, acked, created_at, last_status_at, agent_session_id, tmux_socket, tmux_pane_id, agent_launched_at, retired_agent_session_id, pending_inputs, pending_claimed, parent_id, `+spawnerColumnOf("sessions")+`, launch_prompt, model, account, name_source, priority_tier, role, replaced_by, `+migrationColumn+`, `+migrationOpeningColumn+`
		 FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &sess.Name, &sess.Tool, &sess.Cwd, &sess.Group,
		&sess.Status, &archived, &archivedAt, &acked, &created, &lastStatus, &sess.AgentSessionID,
		&sess.TmuxSocket, &sess.TmuxPaneID,
		&agentLaunched, &sess.RetiredAgentSessionID, &pendingInputs, &pendingClaimed, &sess.ParentID, &sess.SpawnedBy, &sess.LaunchPrompt, &sess.Model, &sess.Account, &sess.NameSource, &tier, &sess.Role, &sess.ReplacedBy, &sess.MigrationID, &migrationOpening)
	if err != nil {
		return Session{}, err
	}
	sess.Priority = priority.Tier(tier)
	if err := json.Unmarshal([]byte(migrationOpening), &sess.MigrationOpening); err != nil {
		return Session{}, fmt.Errorf("decode migration opening: %w", err)
	}
	if err := json.Unmarshal([]byte(pendingInputs), &sess.PendingInputs); err != nil {
		return Session{}, fmt.Errorf("decode pending inputs for session %s: %w", sess.ID, err)
	}
	sess.Archived = archived != 0
	sess.ArchivedAt = decodeTime(archivedAt)
	sess.Acked = acked != 0
	sess.PendingInputClaimed = pendingClaimed != 0
	sess.CreatedAt = decodeTime(created)
	sess.LastStatusAt = decodeTime(lastStatus)
	sess.AgentLaunchedAt = decodeTime(agentLaunched)
	return sess, nil
}

func (s *Store) Children(parentID string) ([]Session, error) {
	if parentID == "" {
		return nil, nil
	}
	sessions, err := s.ListSessions(true)
	if err != nil {
		return nil, err
	}
	var kids []Session
	for _, sess := range sessions {
		if sess.ParentID == parentID {
			kids = append(kids, sess)
		}
	}
	return kids, nil
}

// ArchivedChildCounts is every parent's archived-child count in one query.
// The list drops archived rows, so a parent whose children have all been
// swept up would otherwise lose them from its badge without ever saying so.
func (s *Store) ArchivedChildCounts() (map[string]int, error) {
	if !tracing.Enabled() {
		return s.archivedChildCounts()
	}
	start := time.Now()
	counts, err := s.archivedChildCounts()
	recordOp("store.ArchivedChildCounts", start, err, false, tracing.Attr{Key: "rows", Value: len(counts)})
	return counts, err
}

func (s *Store) archivedChildCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT parent_id, done FROM child_archives`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

// ArchivableChild is a child the sweep may file, with the reason it may.
type ArchivableChild struct {
	ID     string
	Reason string
}

const (
	// ArchiveReasonReported is an exited child whose report to its parent
	// was delivered.
	ArchiveReasonReported = "pane exited and its report was delivered"
	// ArchiveReasonWindow is an exited child nobody heard from for the
	// configured window.
	ArchiveReasonWindow = "pane exited and the auto-archive window passed"
)

// AutoArchivableChildren is the children the list would rather stop drawing:
// a dead child -- its pane has exited -- whose report to its parent has been
// delivered, or one that has been dead longer than the window. Both tests
// are in SQL because the poller runs this on every pass.
//
// Dead is the only status that qualifies. A finished, idle or acked child
// still has an agent in its pane, and the sweep once ended those the moment
// a report landed: eight working children of one parent were killed seconds
// after sending an interim update and coming to rest. The sweep files what
// has already exited; it does not decide that a live agent is over.
//
// Only a report sent by the agent now in the pane counts. A revived child
// still has every report its earlier life sent sitting in its parent's inbox,
// delivered, and reading those made the sweep end the new life the first
// time it came to rest -- a minute after the revive that asked for it back.
func (s *Store) AutoArchivableChildren(before time.Time) ([]ArchivableChild, error) {
	rows, err := s.db.Query(
		`SELECT s.id,
		        EXISTS (
		          SELECT 1 FROM session_inbox i
		          WHERE i.session_id = `+spawnerColumnOf("s")+` AND i.sender_id = s.id
		            AND i.delivered_at != 0
		            AND i.sent_at >= CASE WHEN s.agent_launched_at = 0
		                                  THEN s.created_at ELSE s.agent_launched_at END
		        ) AS reported
		 FROM sessions s
		 WHERE s.parent_id != '' AND s.archived = 0
		   AND s.status = 'dead'
		   AND (reported OR (s.last_status_at != 0 AND s.last_status_at <= ?))
		 ORDER BY s.id`, encodeTime(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var children []ArchivableChild
	for rows.Next() {
		var child ArchivableChild
		var reported bool
		if err := rows.Scan(&child.ID, &reported); err != nil {
			return nil, err
		}
		child.Reason = ArchiveReasonWindow
		if reported {
			child.Reason = ArchiveReasonReported
		}
		children = append(children, child)
	}
	return children, rows.Err()
}

func (s *Store) ClaimPendingInput(id, expected string) (bool, error) {
	encoded, inputs, claimed, err := s.pendingInputState(id)
	if err != nil {
		return false, err
	}
	if claimed || len(inputs) == 0 || inputs[0] != expected {
		return false, nil
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET pending_claimed = 1
		 WHERE id = ? AND pending_inputs = ? AND pending_claimed = 0`, id, encoded)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, s.requireRowOrNoop(res, id)
	}
	return true, nil
}

func (s *Store) ConsumeClaimedPendingInput(id, expected string) (bool, error) {
	encoded, inputs, claimed, err := s.pendingInputState(id)
	if err != nil {
		return false, err
	}
	if !claimed || len(inputs) == 0 || inputs[0] != expected {
		return false, nil
	}
	remaining, err := encodePendingInputs(inputs[1:])
	if err != nil {
		return false, err
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET pending_inputs = ?, pending_claimed = 0
		 WHERE id = ? AND pending_inputs = ? AND pending_claimed = 1`,
		remaining, id, encoded)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, s.requireRowOrNoop(res, id)
	}
	return true, nil
}

func (s *Store) pendingInputState(id string) (string, []string, bool, error) {
	var encoded string
	var claimed int
	if err := s.db.QueryRow(
		`SELECT pending_inputs, pending_claimed FROM sessions WHERE id = ?`, id,
	).Scan(&encoded, &claimed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, false, fmt.Errorf("session %s: %w", id, ErrSessionGone)
		}
		return "", nil, false, err
	}
	var inputs []string
	if err := json.Unmarshal([]byte(encoded), &inputs); err != nil {
		return "", nil, false, fmt.Errorf("decode pending inputs for session %s: %w", id, err)
	}
	return encoded, inputs, claimed != 0, nil
}

func encodePendingInputs(inputs []string) (string, error) {
	if len(inputs) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(inputs)
	return string(encoded), err
}

func (s *Store) UpdateStatus(id, newStatus string) error {
	if !tracing.Enabled() {
		return s.updateStatus(id, newStatus)
	}
	start := time.Now()
	err := s.updateStatus(id, newStatus)
	recordOp("store.UpdateStatus", start, err, true, tracing.Attr{Key: "status", Value: newStatus})
	return err
}

func (s *Store) updateStatus(id, newStatus string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET status = ?, last_status_at = ? WHERE id = ?`,
		newStatus, encodeTime(time.Now()), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// DerivedState is one session's row state as a poll pass derived it, for
// ApplyDerivedStates. An empty Status leaves the status column alone and a nil
// Acked leaves the acked column alone, so a pass can queue either or both
// without reading the row back.
type DerivedState struct {
	ID     string
	Status string
	Acked  *bool
}

// ApplyDerivedStates writes a poll pass's derived row state in one
// transaction, stamping every status it moves at `at`.
//
// These were a statement each, in autocommit. Every writer on this database
// contends for the same write lock -- the poller, each session's mcp process,
// the hooks -- and a waiting writer pays the full hold of whoever is in front
// of it on every acquisition, so a pass that moved twenty rows took that wait
// twenty times over. What made it visible is that the rows move in bursts: a
// stall anywhere upstream leaves half the board looking different on the pass
// after it, and that is exactly the pass that could least afford twenty waits.
// One transaction pays for one. Measured against forty concurrent writers,
// twenty status writes went from a 810ms 95th percentile to 320us.
//
// A row deleted since the pass listed it is skipped rather than failing the
// batch, which is what every caller already did with the single-row writes it
// replaces (see ignoreDeletedSession).
func (s *Store) ApplyDerivedStates(at time.Time, states []DerivedState) error {
	if !tracing.Enabled() {
		return s.applyDerivedStates(at, states)
	}
	start := time.Now()
	err := s.applyDerivedStates(at, states)
	// The row count is what makes this span readable: the transaction's cost
	// is the write lock it waited for rather than the statements it ran, so a
	// pass that moved two rows and a pass that moved twenty are the same span
	// until this tells them apart.
	recordOp("store.ApplyDerivedStates", start, err, true, tracing.Attr{Key: "rows", Value: len(states)})
	return err
}

func (s *Store) applyDerivedStates(at time.Time, states []DerivedState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statuses, err := tx.Prepare(`UPDATE sessions SET status = ?, last_status_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer statuses.Close()
	acks, err := tx.Prepare(`UPDATE sessions SET acked = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer acks.Close()
	stamped := encodeTime(at)
	for _, state := range states {
		if state.Status != "" {
			if _, err := statuses.Exec(state.Status, stamped, state.ID); err != nil {
				return err
			}
		}
		if state.Acked != nil {
			if _, err := acks.Exec(boolToInt(*state.Acked), state.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// AcknowledgeFinished atomically marks a session idle and acked if its stored
// status is still finished. A newer status makes the operation a no-op.
func (s *Store) AcknowledgeFinished(id string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET status = ?, acked = 1, last_status_at = ? WHERE id = ? AND status = ?`,
		status.Idle, encodeTime(time.Now()), id, status.Finished)
	if err != nil {
		return err
	}
	return s.requireRowOrNoop(res, id)
}

// SetAcked marks whether the user has acknowledged the session's last
// finished turn; an acked session renders idle even while its pane still
// shows the finished turn.
func (s *Store) SetAcked(id string, acked bool) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET acked = ? WHERE id = ?`, boolToInt(acked), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// SetAgentSessionID records the agent CLI's own conversation id for a
// session, so a later revive resumes that exact conversation. Used both
// when launching a tool we assign the id to and when capturing the id a
// tool minted itself.
func (s *Store) SetAgentSessionID(id, agentSessionID string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET agent_session_id = ? WHERE id = ?`, agentSessionID, id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// BindAgentSessionID records a captured conversation id, but only while the
// session is still the launch the capture ran for: unbound, and launched at
// the moment the capturing pass read. It reports whether the write landed.
// Capture reads a tool's store from a snapshot and can take minutes, long
// enough for a restart to clear the id and move the launch on underneath it,
// and that stale answer names the conversation the restart just dropped.
func (s *Store) BindAgentSessionID(id, agentSessionID string, launchedAt time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE sessions SET agent_session_id = ?
		 WHERE id = ? AND agent_session_id = '' AND agent_launched_at = ?`,
		agentSessionID, id, encodeTime(launchedAt))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// RestartAgent rebinds a session to a fresh agent run: the conversation it
// was resuming is retired, the new one (empty until capture for tools that
// mint their own id) takes its place, and the launch clock moves to now.
func (s *Store) RestartAgent(id, agentSessionID string, launchedAt time.Time) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET
			retired_agent_session_id = CASE WHEN agent_session_id != '' THEN agent_session_id ELSE retired_agent_session_id END,
			agent_session_id = ?,
			agent_launched_at = ?
		 WHERE id = ?`, agentSessionID, encodeTime(launchedAt), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// SetAgentLaunchedAt records when the agent now in the pane started, without
// touching the conversation it is resuming.
func (s *Store) SetAgentLaunchedAt(id string, launchedAt time.Time) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET agent_launched_at = ? WHERE id = ?`,
		encodeTime(launchedAt), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// SetSnapshot stores the session's final pane capture, kept out of the
// Session struct so list queries never haul the blob.
func (s *Store) SetSnapshot(id, snapshot string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET snapshot = ? WHERE id = ?`, snapshot, id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// SetSnapshots stores a whole batch of final pane captures in one
// transaction.
//
// Every writer on this database contends for the same write lock -- the
// poller, each session's mcp process, the hooks -- so a commit waits about as
// long carrying twenty rows as carrying one. Archiving a group used to pay
// that wait once per session for the captures alone.
//
// Rows the store no longer has are skipped rather than failing the batch: a
// session deleted while its dialog sat open is not a reason to lose the
// captures of the ones beside it.
func (s *Store) SetSnapshots(snapshots map[string]string) error {
	if len(snapshots) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE sessions SET snapshot = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, snapshot := range snapshots {
		if _, err := stmt.Exec(snapshot, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Snapshot(id string) (string, error) {
	var snapshot string
	err := s.db.QueryRow(`SELECT snapshot FROM sessions WHERE id = ?`, id).Scan(&snapshot)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return snapshot, err
}

// SetArchived files a session away or brings it back, stamping the archive
// clock on the way in and clearing it on the way out. The stamp is what
// ExpiredArchives measures, so a restore genuinely resets the window: a
// row filed, restored on day six and filed again gets a fresh seven days
// rather than one.
func (s *Store) SetArchived(id string, archived bool) error {
	if !tracing.Enabled() {
		return s.setArchived(id, archived)
	}
	start := time.Now()
	err := s.setArchived(id, archived)
	// Restoring costs more than filing away -- it reads the row back and
	// walks the group's ancestors -- so the direction is on the span.
	recordOp("store.SetArchived", start, err, true, tracing.Attr{Key: "archived", Value: archived})
	return err
}

func (s *Store) setArchived(id string, archived bool) error {
	stamp := int64(0)
	if archived {
		stamp = encodeTime(time.Now())
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET archived = ?, archived_at = ? WHERE id = ?`, boolToInt(archived), stamp, id)
	if err != nil {
		return err
	}
	if err := requireRow(res, id); err != nil {
		return err
	}
	if err := s.tallyChildArchive(id, archived); err != nil {
		return err
	}
	// Restoring a session out of an archived group must leave it with a live
	// home, so un-archive its group and every ancestor.
	if !archived {
		sess, err := s.Get(id)
		if err != nil {
			return err
		}
		return s.unarchiveAncestorGroups(sess.Group)
	}
	return nil
}

// ArchiveKilled files a batch of sessions away in one transaction, marking
// dead the ones whose pane this archive actually ended. It reports the ids
// that matched no row.
//
// One transaction rather than the two writes per session an archive used to
// make, for the same reason SetSnapshots is one: what an archive costs is
// waiting for the write lock, not the rows it carries.
//
// killed is separate from ids because archiving a session that was not
// running must not rewrite the status it stopped on -- a row filed away
// reading finished comes back reading finished. Only the ids in it were
// killed here, so only those become dead.
//
// A row that went out from under the dialog is returned rather than raised,
// and the rest of the batch still commits: its pane is already dead, and
// failing the transaction over it would leave every session beside it in the
// active list with nothing running behind it. The caller reports it.
func (s *Store) ArchiveKilled(ids []string, killed []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	dead := make(map[string]bool, len(killed))
	for _, id := range killed {
		dead[id] = true
	}
	stamp := encodeTime(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	file, err := tx.Prepare(`UPDATE sessions SET archived = 1, archived_at = ? WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	end, err := tx.Prepare(
		`UPDATE sessions SET status = ?, last_status_at = ?, archived = 1, archived_at = ? WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer end.Close()
	tally, err := tx.Prepare(
		`INSERT INTO child_archives (parent_id, done) VALUES (?, 1)
		 ON CONFLICT(parent_id) DO UPDATE SET done = done + 1`)
	if err != nil {
		return nil, err
	}
	defer tally.Close()
	var missing []string
	for _, id := range ids {
		var res sql.Result
		if dead[id] {
			res, err = end.Exec(status.Dead, stamp, stamp, id)
		} else {
			res, err = file.Exec(stamp, id)
		}
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			missing = append(missing, id)
			continue
		}
		// Counted per child filed, not per row still present, so the tally
		// survives the retention sweep that later deletes the child.
		var parentID string
		if err := tx.QueryRow(`SELECT parent_id FROM sessions WHERE id = ?`, id).Scan(&parentID); err != nil {
			return nil, err
		}
		if parentID == "" {
			continue
		}
		if _, err := tally.Exec(parentID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return missing, nil
}

// tallyChildArchive moves a parent's done count as one child is filed or
// restored, so "(+N done)" counts what happened rather than what is left.
func (s *Store) tallyChildArchive(id string, archived bool) error {
	var parentID string
	err := s.db.QueryRow(`SELECT parent_id FROM sessions WHERE id = ?`, id).Scan(&parentID)
	if errors.Is(err, sql.ErrNoRows) || parentID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if archived {
		_, err = s.db.Exec(
			`INSERT INTO child_archives (parent_id, done) VALUES (?, 1)
			 ON CONFLICT(parent_id) DO UPDATE SET done = done + 1`, parentID)
		return err
	}
	_, err = s.db.Exec(
		`UPDATE child_archives SET done = done - 1 WHERE parent_id = ? AND done > 0`, parentID)
	return err
}

// unarchiveAncestorGroups clears the archived flag on a group path and each
// of its ancestors, leaving descendants untouched.
func (s *Store) unarchiveAncestorGroups(path string) error {
	var err error
	eachAncestor(path, func(ancestor string) bool {
		_, err = s.db.Exec(`UPDATE groups SET archived = 0 WHERE name = ?`, ancestor)
		return err == nil
	})
	return err
}

// ExpiredArchives lists the archived sessions whose stay has run past the
// retention window. It selects rather than deletes: a row's pane and hook
// files live outside this store, so the caller that owns those does the
// removing, through the same teardown every other path uses. A row with
// no stamp is never expired -- see the archived_at migration.
func (s *Store) ExpiredArchives(cutoff time.Time) ([]Session, error) {
	sessions, err := s.ListSessions(true)
	if err != nil {
		return nil, err
	}
	var expired []Session
	for _, sess := range sessions {
		if !sess.Archived || sess.ArchivedAt.IsZero() {
			continue
		}
		if sess.ArchivedAt.Before(cutoff) {
			expired = append(expired, sess)
		}
	}
	return expired, nil
}

// Delete removes a session and the coordination state that only makes
// sense while it exists. One transaction, because a session row that
// outlives its own inbox strands every sender waiting on a receipt.
func (s *Store) Delete(id string) error {
	if !tracing.Enabled() {
		return s.deleteSession(id)
	}
	start := time.Now()
	err := s.deleteSession(id)
	recordOp("store.Delete", start, err, true)
	return err
}

func (s *Store) deleteSession(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Session ids are recycled from a fresh UUID prefix, so a message left
	// pointing at a deleted id could be re-attached to a future session.
	if _, err := tx.Exec(`DELETE FROM session_inbox WHERE session_id = ? OR sender_id = ?`, id, id); err != nil {
		return err
	}
	// A claim outlives its holder as pending work rather than as a task
	// parked forever against a session that no longer exists.
	if err := releaseTasksOwnedBy(tx, id, time.Now()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM file_reservations WHERE session_id = ?`, id); err != nil {
		return err
	}
	if err := deleteOpenedPRs(tx, id); err != nil {
		return err
	}
	if err := unlinkMigration(tx, id); err != nil {
		return err
	}
	if err := unlinkRetiredRoles(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM replace_holds WHERE fresh_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := requireRow(res, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteChild removes a session only while it still hangs under parentID.
// kill runs inside the same write transaction, which holds the database's
// writer lock, so a placement cannot slip between the ownership check and
// the delete, and a failed kill rolls the row back into place.
func (s *Store) DeleteChild(id, parentID string, kill func() error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE sessions SET parent_id = ? WHERE id = ? AND parent_id = ?`, parentID, id, parentID)
	if err != nil {
		return err
	}
	held, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if held == 0 {
		return fmt.Errorf("session %s is no longer nested under %s", id, parentID)
	}
	if err := kill(); err != nil {
		return err
	}
	if err := deleteOpenedPRs(tx, id); err != nil {
		return err
	}
	if err := unlinkMigration(tx, id); err != nil {
		return err
	}
	if err := unlinkRetiredRoles(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SessionsInSubtree returns every session (archived included) whose group
// is the given path or any descendant of it.
func (s *Store) SessionsInSubtree(path string) ([]Session, error) {
	sessions, err := s.ListSessions(true)
	if err != nil {
		return nil, err
	}
	var matched []Session
	for _, sess := range sessions {
		if inSubtree(sess.Group, path) {
			matched = append(matched, sess)
		}
	}
	return matched, nil
}

// RenameGroup rewrites a group path and every descendant group and
// session under it. Fails if the destination path already exists.
func (s *Store) RenameGroup(oldPath, newPath string) error {
	if oldPath == "" || newPath == "" {
		return fmt.Errorf("group path cannot be empty")
	}
	if oldPath == newPath {
		return nil
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM groups WHERE name = ?)`, newPath).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 1 {
		return fmt.Errorf("group %s already exists", newPath)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	likeOld := escapeLike(oldPath)
	_, err = tx.Exec(
		`UPDATE groups SET name = ? || substr(name, length(?)+1)
		 WHERE name = ? OR name LIKE ? || '/%' ESCAPE '\'`,
		newPath, oldPath, oldPath, likeOld)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`UPDATE sessions SET group_name = ? || substr(group_name, length(?)+1)
		 WHERE group_name = ? OR group_name LIKE ? || '/%' ESCAPE '\'`,
		newPath, oldPath, oldPath, likeOld)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MoveGroup re-parents a group subtree under newParent ("" = root),
// keeping its base name. Every descendant group and session follows.
func (s *Store) MoveGroup(path, newParent string) error {
	if path == "" {
		return fmt.Errorf("group path cannot be empty")
	}
	if inSubtree(newParent, path) {
		return fmt.Errorf("cannot move %s into its own subtree", path)
	}
	newPath := path[strings.LastIndex(path, "/")+1:]
	if newParent != "" {
		newPath = newParent + "/" + newPath
	}
	return s.RenameGroup(path, newPath)
}

// validParent reads the parent through the caller's transaction, so the row
// it approves cannot be deleted or nested before the write lands.
// leaf says the row being filed can never itself become a parent, which is
// what lets a shell sit one level deeper than an ordinary child: the depth
// cap exists to stop the tree growing, and a leaf cannot grow it.
func validParent(tx *sql.Tx, id, parentID string, leaf bool) (string, error) {
	if parentID == id {
		return "", fmt.Errorf("session %s cannot be its own parent", id)
	}
	var group, grandparent string
	err := tx.QueryRow(`SELECT group_name, parent_id FROM sessions WHERE id = ?`, parentID).Scan(&group, &grandparent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("parent %s: %w", parentID, err)
	}
	if err != nil {
		return "", err
	}
	if grandparent != "" && !leaf {
		return "", fmt.Errorf("parent %s already has a parent", parentID)
	}
	return group, nil
}

func (s *Store) PlaceSession(id, group, parentID string) error {
	parentID = strings.TrimSpace(parentID)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM sessions WHERE id = ? OR (
		`+migrationColumn+` != '' AND `+migrationColumn+` =
		(SELECT value FROM settings WHERE key = ?)) ORDER BY sort_order, created_at`, id, migrationKeyPrefix+id)
	if err != nil {
		return err
	}
	var members []string
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			rows.Close()
			return err
		}
		members = append(members, memberID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(members) == 0 {
		return fmt.Errorf("session %s: %w", id, ErrSessionGone)
	}
	for _, memberID := range members {
		if parentID != "" {
			parentGroup, err := validParent(tx, memberID, parentID, false)
			if err != nil {
				return err
			}
			group = parentGroup
		}
		if parentID != "" {
			var kids int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE parent_id = ?`, memberID).Scan(&kids); err != nil {
				return err
			}
			if kids > 0 {
				return fmt.Errorf("session %s has terminals of its own; move them out first", memberID)
			}
		}
		// spawned_by moves with parent_id here, and only here. A placement is
		// somebody saying in as many words who this row belongs to -- an
		// agent claiming a spawn the tools lost, an operator dragging a row
		// under a session, a release taking one back to the top level -- and
		// that is a statement about ownership, not only about where the board
		// draws it. Keeping the two in step is also what the one column did
		// before they were split, so adopting still confers the right to
		// answer the row and releasing still gives it up.
		res, err := tx.Exec(
			`UPDATE sessions SET group_name = ?, parent_id = ?, spawned_by = ?,
		 sort_order = (SELECT COALESCE(MAX(sort_order)+1, 0) FROM sessions WHERE group_name = ? AND parent_id = ?)
		 WHERE id = ?`,
			group, parentID, parentID, group, parentID, memberID)
		if err != nil {
			return err
		}
		if err := requireRow(res, memberID); err != nil {
			return err
		}
		if parentID == "" {
			if _, err := tx.Exec(`UPDATE sessions SET group_name = ? WHERE parent_id = ?`, group, memberID); err != nil {
				return err
			}
		}
	}
	if group != "" {
		if _, err := tx.Exec(
			`INSERT INTO groups (name, sort_order)
			 VALUES (?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
			 ON CONFLICT(name) DO NOTHING`, group); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MoveSession(id, group string) error {
	return s.PlaceSession(id, group, "")
}

// RenameSession records a name a person chose.
func (s *Store) RenameSession(id, name string) error {
	return s.RenameSessionAs(id, name, SourceUser)
}

// RenameSessionAs renames a session and records who chose the name.
func (s *Store) RenameSessionAs(id, name, source string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	res, err := s.db.Exec(`UPDATE sessions SET name = ?, name_source = ? WHERE id = ?`,
		name, nameSourceOr(source), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// AutoRenameSession replaces a name the manager derived, and refuses to touch
// one anybody else chose. It reports whether the row was taken.
//
// The check is in the UPDATE rather than in a read before it on purpose: the
// user renaming a row is a keystroke, the naming pass is a ticker, and a
// read-then-write would lose that race silently and rarely, which is the worst
// way to lose it.
func (s *Store) AutoRenameSession(id, name, source string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, fmt.Errorf("session name cannot be empty")
	}
	if source != SourceDerived && source != SourceTitle {
		return false, fmt.Errorf("automatic rename cannot claim source %q", source)
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET name = ?, name_source = ? WHERE id = ? AND `+autoNamable,
		name, source, id)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// nameSourceOr keeps an unset source out of the column, so a row written by
// code that predates it is not silently made renamable.
func nameSourceOr(source string) string {
	switch source {
	case SourceDerived, SourceTitle, SourceAgent, SourceUser:
		return source
	}
	return SourceUser
}

// SetTmuxTarget records the tmux server and pane id of a pane the manager
// adopted, or clears both to hand the session back to the managed naming.
// Adoption can happen after the row exists, so the pair needs a write path
// of its own rather than only travelling through session creation.
func (s *Store) SetTmuxTarget(id, socket, paneID string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET tmux_socket = ?, tmux_pane_id = ? WHERE id = ?`,
		socket, paneID, id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// UpdateTool changes which tool status rules and revive use for a session.
// Clears the captured agent conversation id: that id only makes sense for
// the tool that minted it, and a manual tool swap means the user swapped
// the process in the pane (e.g. quit opencode, ran vim). A no-op when the
// tool column already matches leaves the conversation id alone.
func (s *Store) UpdateTool(id, tool string) error {
	if strings.TrimSpace(tool) == "" {
		return fmt.Errorf("session tool cannot be empty")
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET tool = ?, agent_session_id = '' WHERE id = ? AND tool != ?`,
		tool, id, tool)
	if err != nil {
		return err
	}
	return s.requireRowOrNoop(res, id)
}

// DeleteGroup removes a group and all its descendant groups, reporting
// the paths it removed.
func (s *Store) DeleteGroup(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("cannot delete the root group")
	}
	groups, err := s.Groups()
	if err != nil {
		return nil, err
	}
	return s.deleteGroups(groups, func(g Group) bool { return inSubtree(g.Name, path) })
}

// ErrGroupNotFound reports a group path no row carries.
var ErrGroupNotFound = errors.New("group does not exist")

// RemoveGroup deletes a group and its descendant groups and moves every
// session held beneath them to the root, in one transaction, so a failure
// partway leaves the tree as it was. A moved session keeps its parent
// link, so a terminal relocated with its agent still hangs under it.
// Reports the group paths removed and the ids of the sessions moved.
func (s *Store) RemoveGroup(path string) (removedGroups, movedSessions []string, err error) {
	if path == "" {
		return nil, nil, fmt.Errorf("cannot delete the root group")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	groupNames, err := txStrings(tx, `SELECT name FROM groups ORDER BY sort_order, name`)
	if err != nil {
		return nil, nil, err
	}
	known := false
	for _, name := range groupNames {
		if name == path {
			known = true
		}
		if inSubtree(name, path) {
			removedGroups = append(removedGroups, name)
		}
	}
	if !known {
		return nil, nil, ErrGroupNotFound
	}
	held, err := txStrings(tx,
		`SELECT id FROM sessions WHERE group_name = ? OR group_name LIKE ? || '/%' ESCAPE '\'
		 ORDER BY group_name, parent_id, sort_order`,
		path, escapeLike(path))
	if err != nil {
		return nil, nil, err
	}
	var nextOrder int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(sort_order)+1, 0) FROM sessions WHERE group_name = ''`,
	).Scan(&nextOrder); err != nil {
		return nil, nil, err
	}
	for _, id := range held {
		if _, err := tx.Exec(
			`UPDATE sessions SET group_name = '', sort_order = ? WHERE id = ?`, nextOrder, id); err != nil {
			return nil, nil, err
		}
		nextOrder++
	}
	for _, name := range removedGroups {
		if _, err := tx.Exec(`DELETE FROM groups WHERE name = ?`, name); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return removedGroups, held, nil
}

func txStrings(tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// PruneArchivedGroups removes the groups in a subtree that are archived,
// directly or through an archived ancestor, and hold no session anywhere
// beneath them, reporting the paths it removed. A group that still holds
// a session stays, and so does each of its ancestors, so no session is
// left without a home. An empty root covers every group.
//
// The retention sweep runs this rather than DeleteGroup: it clears the group
// rows that expiring the archive left behind while the live tree keeps its
// own. A group archived with nothing under it has nothing to wait for and
// goes on the first sweep; one holding archived sessions waits out their
// window with them.
func (s *Store) PruneArchivedGroups(root string) ([]string, error) {
	sessions, err := s.ListSessions(true)
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	for _, sess := range sessions {
		eachAncestor(sess.Group, func(path string) bool {
			occupied[path] = true
			return true
		})
	}
	groups, err := s.Groups()
	if err != nil {
		return nil, err
	}
	archived := map[string]bool{}
	for _, g := range groups {
		if g.Archived {
			archived[g.Name] = true
		}
	}
	return s.deleteGroups(groups, func(g Group) bool {
		return (root == "" || inSubtree(g.Name, root)) && !occupied[g.Name] && EffectivelyArchived(archived, g.Name)
	})
}

// deleteGroups removes every group the predicate selects, reporting the
// paths it removed.
func (s *Store) deleteGroups(groups []Group, selected func(Group) bool) ([]string, error) {
	var removed []string
	for _, g := range groups {
		if !selected(g) {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM groups WHERE name = ?`, g.Name); err != nil {
			return removed, err
		}
		removed = append(removed, g.Name)
	}
	return removed, nil
}

// EffectivelyArchived reports whether a group path counts as archived,
// either directly or because an ancestor group was archived as a whole.
func EffectivelyArchived(archived map[string]bool, path string) bool {
	found := false
	eachAncestor(path, func(ancestor string) bool {
		found = archived[ancestor]
		return !found
	})
	return found
}

// inSubtree reports whether a group path is the root itself or any group
// nested under it.
func inSubtree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// eachAncestor visits a group path and then each of its ancestors,
// stopping early when the visitor returns false.
func eachAncestor(path string, visit func(string) bool) {
	for path != "" {
		if !visit(path) {
			return
		}
		idx := strings.LastIndex(path, "/")
		if idx < 0 {
			return
		}
		path = path[:idx]
	}
}

type Group struct {
	Name     string
	Path     string
	Archived bool
	// Priority tiers the whole subtree: every session filed under the
	// group, or under any group below it, triages at this tier unless it
	// states a higher one of its own.
	Priority priority.Tier
}

func (s *Store) Groups() ([]Group, error) {
	if !tracing.Enabled() {
		return s.groups()
	}
	start := time.Now()
	groups, err := s.groups()
	recordOp("store.Groups", start, err, false, tracing.Attr{Key: "rows", Value: len(groups)})
	return groups, err
}

func (s *Store) groups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT name, path, archived, priority_tier FROM groups ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []Group
	for rows.Next() {
		var g Group
		var archived int
		var tier string
		if err := rows.Scan(&g.Name, &g.Path, &archived, &tier); err != nil {
			return nil, err
		}
		g.Archived = archived != 0
		g.Priority = priority.Tier(tier)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// SetPriority stores a session's tier. Unset clears it.
//
// The retired flag column is zeroed with it, which is what lets the
// backfill in migrate() run on every start: a row whose tier has been
// written by hand is no longer a row the flag has anything to say about.
func (s *Store) SetPriority(id string, tier priority.Tier) error {
	res, err := s.db.Exec(`UPDATE sessions SET priority_tier = ?, priority = 0 WHERE id = ?`, string(tier), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// SetGroupPriority stores a group's tier. It stays on the group row alone:
// the sessions under it read it through EffectiveTier, so a session moved
// in or out of the group takes or sheds it on its own.
func (s *Store) SetGroupPriority(name string, tier priority.Tier) error {
	if name == "" {
		return fmt.Errorf("cannot prioritise the root group")
	}
	res, err := s.db.Exec(`UPDATE groups SET priority_tier = ?, priority = 0 WHERE name = ?`, string(tier), name)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("group %q: %w", name, ErrGroupNotFound)
	}
	return nil
}

// EffectiveTier is the tier a session actually triages at: its own, or the
// highest of any group above it. Same shape as EffectivelyArchived, for the
// same reason -- a group's tier covers its subtree.
//
// The highest wins rather than the nearest, so filing a session under an
// urgent group cannot quietly demote it and neither can the group's own
// tier be escaped by stating a lower one on the session. Walking from the
// session outwards keeps the nearer statement on top of a tie; see
// priority.Better.
func EffectiveTier(groupTiers map[string]priority.Tier, sess Session) priority.Tier {
	tier := sess.Priority
	for path := sess.Group; path != ""; path = parentPath(path) {
		tier = priority.Better(tier, groupTiers[path])
	}
	return tier
}

// SetGroupArchived flips the archived flag on a group, every descendant
// group, and every session in the subtree, in one transaction.
func (s *Store) SetGroupArchived(path string, archived bool) error {
	if path == "" {
		return fmt.Errorf("cannot archive the root group")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	flag := boolToInt(archived)
	likePath := escapeLike(path)
	if _, err := tx.Exec(
		`UPDATE groups SET archived = ? WHERE name = ? OR name LIKE ? || '/%' ESCAPE '\'`,
		flag, path, likePath); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET archived = ? WHERE group_name = ? OR group_name LIKE ? || '/%' ESCAPE '\'`,
		flag, path, likePath); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderSession moves a session one visible step among its group
// siblings, reporting whether anything moved. Hidden (archived)
// siblings are skipped when the caller's view excludes them, so a move
// always has a visible effect. Siblings are renumbered to a dense 0..n
// first, since fresh databases start with ties.
func (s *Store) ReorderSession(id string, delta int, includeArchived bool) (bool, error) {
	sess, err := s.Get(id)
	if err != nil {
		return false, err
	}
	sessions, err := s.ListSessions(includeArchived)
	if err != nil {
		return false, err
	}
	var siblings []Session
	current := -1
	for _, candidate := range sessions {
		if candidate.Group == sess.Group && candidate.ParentID == sess.ParentID {
			if candidate.ID == id {
				current = len(siblings)
			}
			siblings = append(siblings, candidate)
		}
	}
	if current < 0 {
		return false, fmt.Errorf("session %s not found among its siblings", id)
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := current + step; i >= 0 && i < len(siblings); i += step {
		if Linked(sess, siblings[i]) {
			continue
		}
		if err := s.SwapSessionOrder(id, siblings[i].ID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// SwapSessionOrder exchanges two sessions in the same group. The caller can
// choose visible siblings even when filtered sessions sit between them.
func (s *Store) SwapSessionOrder(id, targetID string) error {
	sess, err := s.Get(id)
	if err != nil {
		return err
	}
	target, err := s.Get(targetID)
	if err != nil {
		return err
	}
	if sess.Group != target.Group || sess.ParentID != target.ParentID {
		return fmt.Errorf("sessions %s and %s are not siblings", id, targetID)
	}

	sessions, err := s.ListSessions(true)
	if err != nil {
		return err
	}
	var siblings []Session
	for _, candidate := range sessions {
		if candidate.Group == sess.Group && candidate.ParentID == sess.ParentID {
			siblings = append(siblings, candidate)
		}
	}
	ordered, err := SwapLinkedSessions(siblings, id, targetID)
	if err != nil {
		return err
	}
	ids := make([]string, len(ordered))
	for i, sibling := range ordered {
		ids[i] = sibling.ID
	}
	return s.persistSessionOrder(ids)
}

func (s *Store) persistSessionOrder(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE sessions SET sort_order = ? WHERE id = ?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReorderGroup moves a group one step among the groups sharing its
// parent path, reporting whether anything moved. All groups are
// renumbered to their current global order first so sibling swaps are
// well-defined.
func (s *Store) ReorderGroup(path string, delta int) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("cannot reorder the root group")
	}
	if err := s.ensureGroup(path); err != nil {
		return false, err
	}
	groups, err := s.Groups()
	if err != nil {
		return false, err
	}
	parent := ""
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		parent = path[:idx]
	}
	isSibling := func(name string) bool {
		if parent == "" {
			return !strings.Contains(name, "/")
		}
		rest, ok := strings.CutPrefix(name, parent+"/")
		return ok && !strings.Contains(rest, "/")
	}

	current, target := -1, -1
	for i, g := range groups {
		if g.Name == path {
			current = i
			break
		}
	}
	if current < 0 {
		return false, fmt.Errorf("group %s not found", path)
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := current + step; i >= 0 && i < len(groups); i += step {
		if isSibling(groups[i].Name) {
			target = i
			break
		}
	}
	if target < 0 {
		return false, nil
	}

	groups[current], groups[target] = groups[target], groups[current]
	if err := s.persistGroupOrder(groups); err != nil {
		return false, err
	}
	return true, nil
}

// SwapGroupOrder exchanges two groups with the same parent. siblingOrder
// materializes displayed ancestors so their manual order can persist too.
func (s *Store) SwapGroupOrder(path, targetPath string, siblingOrder ...string) error {
	if path == "" || targetPath == "" {
		return fmt.Errorf("cannot reorder the root group")
	}
	parent := parentPath(path)
	if parent != parentPath(targetPath) {
		return fmt.Errorf("groups %s and %s are not siblings", path, targetPath)
	}
	for _, sibling := range siblingOrder {
		if parentPath(sibling) != parent {
			return fmt.Errorf("group %s is not a sibling of %s", sibling, path)
		}
		if err := s.ensureGroup(sibling); err != nil {
			return err
		}
	}
	groups, err := s.Groups()
	if err != nil {
		return err
	}
	current, target := -1, -1
	for i, group := range groups {
		switch group.Name {
		case path:
			current = i
		case targetPath:
			target = i
		}
	}
	if current < 0 || target < 0 {
		return fmt.Errorf("group order changed while reordering")
	}
	groups[current], groups[target] = groups[target], groups[current]
	return s.persistGroupOrder(groups)
}

func parentPath(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return ""
}

func (s *Store) persistGroupOrder(groups []Group) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, g := range groups {
		if _, err := tx.Exec(`UPDATE groups SET sort_order = ? WHERE name = ?`, i, g.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// requireRowOrNoop resolves a guarded UPDATE that matched zero rows: either
// the WHERE condition already held (success no-op) or the session is gone.
// Distinguish so callers still see ErrSessionGone.
func (s *Store) requireRowOrNoop(res sql.Result, id string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var exists int
	err = s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("session %s: %w", id, ErrSessionGone)
	}
	return err
}

func requireRow(res sql.Result, id string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("session %s: %w", id, ErrSessionGone)
	}
	return nil
}

// escapeLike escapes the LIKE metacharacters so a group path is matched
// literally in a `? || '/%'` prefix pattern, paired with ESCAPE '\'. Group
// names may contain '_' or '%', which LIKE would otherwise treat as
// wildcards and let one group's subtree bleed into another's.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// secondsCeiling separates the two timestamp encodings the sessions table
// has held. Values below it are Unix seconds written before nanosecond
// precision (a seconds timestamp stays under it until the year 33658); at
// or above it are Unix nanoseconds (any real nanosecond timestamp since
// 1970 far exceeds it). This lets decodeTime read old rows without a data
// migration.
const secondsCeiling int64 = 1e12

// encodeTime stores a timestamp as Unix nanoseconds so sessions launched in
// the same second keep a distinct, ordered launch time. The zero time
// encodes as 0.
func encodeTime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// decodeTime reverses encodeTime, reading pre-precision rows as seconds.
func decodeTime(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	if v < secondsCeiling {
		return time.Unix(v, 0)
	}
	return time.Unix(0, v)
}

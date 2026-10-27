// Package store persists sessions, their event history, attachments and the
// recently used working directories in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is the SQLite database.
type Store struct {
	db *sql.DB
}

// migrations run in order; the index+1 is stored as PRAGMA user_version.
var migrations = []string{
	`CREATE TABLE sessions (
		id               TEXT PRIMARY KEY,
		provider         TEXT NOT NULL,
		cwd              TEXT NOT NULL,
		title            TEXT NOT NULL DEFAULT '',
		cli_session_id   TEXT NOT NULL DEFAULT '',
		has_conversation INTEGER NOT NULL DEFAULT 0,
		model            TEXT NOT NULL DEFAULT '',
		effort           TEXT NOT NULL DEFAULT '',
		mode             TEXT NOT NULL DEFAULT '',
		options          TEXT NOT NULL DEFAULT '{}',
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL,
		last_active_at   INTEGER NOT NULL
	);
	CREATE TABLE events (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		seq        INTEGER NOT NULL,
		type       TEXT NOT NULL,
		time       INTEGER NOT NULL,
		data       TEXT NOT NULL,
		PRIMARY KEY (session_id, seq)
	) WITHOUT ROWID;
	CREATE TABLE attachments (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		id         TEXT NOT NULL,
		name       TEXT NOT NULL,
		media_type TEXT NOT NULL,
		size       INTEGER NOT NULL DEFAULT 0,
		path       TEXT NOT NULL DEFAULT '',
		data       BLOB,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (session_id, id)
	);
	CREATE TABLE workdirs (
		path         TEXT PRIMARY KEY,
		last_used_at INTEGER NOT NULL,
		use_count    INTEGER NOT NULL DEFAULT 0
	);`,
}

// Open opens (and creates or migrates) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One connection serializes writers; the load is a handful of users.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v) }

// --- sessions ----------------------------------------------------------------

// Session is a persisted session.
type Session struct {
	ID       string
	Provider string
	Cwd      string
	Title    string
	// CLISessionID is the CLI's own conversation id, used for --resume.
	CLISessionID string
	// HasConversation is set once a prompt was submitted, i.e. the CLI has a
	// transcript that --resume can load.
	HasConversation bool
	Model           string
	Effort          string
	Mode            string
	// Options is the JSON of provider-specific launch options.
	Options      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastActiveAt time.Time
}

const sessionCols = `id, provider, cwd, title, cli_session_id, has_conversation, model, effort, mode, options, created_at, updated_at, last_active_at`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var r Session
	var created, updated, active int64
	err := row.Scan(&r.ID, &r.Provider, &r.Cwd, &r.Title, &r.CLISessionID, &r.HasConversation,
		&r.Model, &r.Effort, &r.Mode, &r.Options, &created, &updated, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.CreatedAt, r.UpdatedAt, r.LastActiveAt = fromMS(created), fromMS(updated), fromMS(active)
	return r, err
}

// CreateSession inserts a session.
func (s *Store) CreateSession(ctx context.Context, r Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (`+sessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Provider, r.Cwd, r.Title, r.CLISessionID, r.HasConversation, r.Model, r.Effort, r.Mode, r.Options,
		ms(r.CreatedAt), ms(r.UpdatedAt), ms(r.LastActiveAt))
	return err
}

// UpdateSession writes every mutable column of r.
func (s *Store) UpdateSession(ctx context.Context, r Session) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET title=?, cli_session_id=?, has_conversation=?, model=?, effort=?, mode=?,
		options=?, updated_at=?, last_active_at=? WHERE id=?`,
		r.Title, r.CLISessionID, r.HasConversation, r.Model, r.Effort, r.Mode, r.Options, ms(r.UpdatedAt), ms(r.LastActiveAt), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSessions returns all sessions, most recently active first.
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions ORDER BY last_active_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSession removes a session with its events and attachments.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

// --- events ------------------------------------------------------------------

// EventRow is one stored event. Data is the JSON the API serves.
type EventRow struct {
	Seq  int64
	Type string
	Time time.Time
	Data []byte
}

// AppendEvent stores an event under the given sequence number.
func (s *Store) AppendEvent(ctx context.Context, sessionID string, e EventRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (session_id, seq, type, time, data) VALUES (?,?,?,?,?)`,
		sessionID, e.Seq, e.Type, ms(e.Time), string(e.Data))
	return err
}

// MaxSeq returns the highest event sequence number of a session (0 if none).
func (s *Store) MaxSeq(ctx context.Context, sessionID string) (int64, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM events WHERE session_id=?`, sessionID).Scan(&v)
	return v.Int64, err
}

// EventsAfter returns up to limit events with seq > after, oldest first.
func (s *Store) EventsAfter(ctx context.Context, sessionID string, after int64, limit int) ([]EventRow, error) {
	return s.queryEvents(ctx, `SELECT seq, type, time, data FROM events WHERE session_id=? AND seq>? ORDER BY seq ASC LIMIT ?`,
		sessionID, after, limit)
}

// EventsBefore returns the last limit events with seq < before, oldest first.
func (s *Store) EventsBefore(ctx context.Context, sessionID string, before int64, limit int) ([]EventRow, error) {
	rows, err := s.queryEvents(ctx, `SELECT seq, type, time, data FROM events WHERE session_id=? AND seq<? ORDER BY seq DESC LIMIT ?`,
		sessionID, before, limit)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, err
}

func (s *Store) queryEvents(ctx context.Context, q string, args ...any) ([]EventRow, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var t int64
		var data string
		if err := rows.Scan(&e.Seq, &e.Type, &t, &data); err != nil {
			return nil, err
		}
		e.Time, e.Data = fromMS(t), []byte(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- attachments ---------------------------------------------------------------

// Attachment is a stored file reference. Exactly one of Data and Path is
// normally set: inline content (screenshots) or a file on disk the agent sent.
type Attachment struct {
	ID        string
	Name      string
	MediaType string
	Size      int64
	Path      string
	Data      []byte
	CreatedAt time.Time
}

// PutAttachment stores or replaces an attachment.
func (s *Store) PutAttachment(ctx context.Context, sessionID string, a Attachment) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attachments (session_id, id, name, media_type, size, path, data, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (session_id, id) DO UPDATE SET name=excluded.name, media_type=excluded.media_type, size=excluded.size,
			path=excluded.path, data=COALESCE(excluded.data, attachments.data)`,
		sessionID, a.ID, a.Name, a.MediaType, a.Size, a.Path, a.Data, ms(a.CreatedAt))
	return err
}

// GetAttachment loads one attachment.
func (s *Store) GetAttachment(ctx context.Context, sessionID, id string) (Attachment, error) {
	var a Attachment
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, name, media_type, size, path, data, created_at FROM attachments WHERE session_id=? AND id=?`,
		sessionID, id).Scan(&a.ID, &a.Name, &a.MediaType, &a.Size, &a.Path, &a.Data, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt = fromMS(created)
	return a, err
}

// --- working directories ----------------------------------------------------------

// Workdir is a working directory used before.
type Workdir struct {
	Path       string
	LastUsedAt time.Time
	UseCount   int
}

// TouchWorkdir records a use of path.
func (s *Store) TouchWorkdir(ctx context.Context, path string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workdirs (path, last_used_at, use_count) VALUES (?, ?, 1)
		ON CONFLICT (path) DO UPDATE SET last_used_at=excluded.last_used_at, use_count=use_count+1`, path, ms(at))
	return err
}

// ListWorkdirs returns the recently used directories, most recent first.
func (s *Store) ListWorkdirs(ctx context.Context, limit int) ([]Workdir, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, last_used_at, use_count FROM workdirs ORDER BY last_used_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workdir
	for rows.Next() {
		var w Workdir
		var t int64
		if err := rows.Scan(&w.Path, &t, &w.UseCount); err != nil {
			return nil, err
		}
		w.LastUsedAt = fromMS(t)
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWorkdir forgets a directory.
func (s *Store) DeleteWorkdir(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workdirs WHERE path=?`, path)
	return err
}

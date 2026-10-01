package hub

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure Go driver "sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id               TEXT PRIMARY KEY,
  box              TEXT NOT NULL,
  name             TEXT NOT NULL DEFAULT '',
  agent            TEXT NOT NULL DEFAULT '',
  minutes          INTEGER NOT NULL DEFAULT 0,
  started_at       INTEGER NOT NULL DEFAULT 0,
  ended_at         INTEGER NOT NULL DEFAULT 0,
  end_reason       TEXT NOT NULL DEFAULT '',
  extended_minutes INTEGER NOT NULL DEFAULT 0,
  token            TEXT NOT NULL UNIQUE,
  code             TEXT NOT NULL,
  local_code       TEXT NOT NULL DEFAULT '',
  expires_at       INTEGER NOT NULL,
  archive_path     TEXT NOT NULL DEFAULT '',
  archive_bytes    INTEGER NOT NULL DEFAULT 0,
  empty            INTEGER NOT NULL DEFAULT 0,
  github_repo      TEXT NOT NULL DEFAULT '',
  github_status    TEXT NOT NULL DEFAULT '',
  github_error     TEXT NOT NULL DEFAULT '',
  github_next_at   INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_code ON sessions(code, expires_at);
CREATE INDEX IF NOT EXISTS sessions_started ON sessions(started_at);

CREATE TABLE IF NOT EXISTS boxes (
  name         TEXT PRIMARY KEY,
  last_seen    INTEGER NOT NULL DEFAULT 0,
  state        TEXT NOT NULL DEFAULT '',
  seconds_left INTEGER NOT NULL DEFAULT 0,
  status_json  TEXT NOT NULL DEFAULT '{}',
  session_id   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS requests (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  box        TEXT NOT NULL,
  asked_at   INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  minutes    INTEGER NOT NULL DEFAULT 0,
  decided_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(session_id, asked_at)
);

CREATE TABLE IF NOT EXISTS commands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  box        TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  op         TEXT NOT NULL,
  minutes    INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  picked_at  INTEGER NOT NULL DEFAULT 0,
  ok         INTEGER,
  message    TEXT NOT NULL DEFAULT '',
  done_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS commands_box ON commands(box, picked_at);

CREATE TABLE IF NOT EXISTS enrollments (
  mac              TEXT PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  token            TEXT NOT NULL UNIQUE,
  enrolled_at      INTEGER NOT NULL,
  current_hostname TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS config (
  scope      TEXT NOT NULL,
  name       TEXT NOT NULL,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scope, name)
);
`

// migrations add columns to tables created by an older hub. A "duplicate
// column" error means the column is already there.
var migrations = []string{
	`ALTER TABLE boxes ADD COLUMN applied_config_version TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE boxes ADD COLUMN activity_json TEXT NOT NULL DEFAULT '[]'`,
	`ALTER TABLE sessions ADD COLUMN email TEXT NOT NULL DEFAULT ''`,
}

func openDB(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, "hub.db")
	// The database holds agent keys: create it 0600 before SQLite opens it.
	// SQLite gives the -wal and -shm files the same mode as the database.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serialises writes anyway, and this rules out
	// SQLITE_BUSY between our own goroutines. Never hold rows open across
	// another query.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	for _, side := range []string{"-wal", "-shm"} {
		os.Chmod(path+side, 0o600) // may not exist yet; fine
	}
	return db, nil
}

// Session is one row of the sessions table.
type Session struct {
	ID              string
	Box             string
	Name            string
	Agent           string
	Minutes         int
	StartedAt       int64
	EndedAt         int64
	EndReason       string
	ExtendedMinutes int
	Token           string
	Code            string
	LocalCode       string
	ExpiresAt       int64
	ArchivePath     string
	ArchiveBytes    int64
	Empty           bool
	GitHubRepo      string
	GitHubStatus    string
	GitHubError     string
	GitHubNextAt    int64
	CreatedAt       int64
	Email           string // attendee email, staff only; never on the public page
}

const sessionCols = `id, box, name, agent, minutes, started_at, ended_at, end_reason,
 extended_minutes, token, code, local_code, expires_at, archive_path, archive_bytes, empty,
 github_repo, github_status, github_error, github_next_at, created_at, email`

type scanner interface{ Scan(...any) error }

func scanSession(sc scanner) (*Session, error) {
	var s Session
	var empty int
	err := sc.Scan(&s.ID, &s.Box, &s.Name, &s.Agent, &s.Minutes, &s.StartedAt, &s.EndedAt,
		&s.EndReason, &s.ExtendedMinutes, &s.Token, &s.Code, &s.LocalCode, &s.ExpiresAt,
		&s.ArchivePath, &s.ArchiveBytes, &empty, &s.GitHubRepo, &s.GitHubStatus,
		&s.GitHubError, &s.GitHubNextAt, &s.CreatedAt, &s.Email)
	if err != nil {
		return nil, err
	}
	s.Empty = empty != 0
	return &s, nil
}

var errNotFound = errors.New("not found")

func (s *Server) sessionBy(col string, val any) (*Session, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE `+col+` = ?`, val)
	ss, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return ss, err
}

func (s *Server) recentSessions(limit int) ([]*Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions ORDER BY started_at DESC, created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		ss, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

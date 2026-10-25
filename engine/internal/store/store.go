// Package store persists jobs in a SQLite database (pure Go driver).
package store

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id      TEXT PRIMARY KEY,
	status  TEXT NOT NULL,
	created INTEGER NOT NULL,
	updated INTEGER NOT NULL,
	data    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_created ON jobs(created);
`

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveJob(id, status string, created, updated int64, data []byte) error {
	_, err := s.db.Exec(`INSERT INTO jobs(id,status,created,updated,data) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status, updated=excluded.updated, data=excluded.data`,
		id, status, created, updated, string(data))
	return err
}

func (s *Store) DeleteJob(id string) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

// LoadJobs returns the newest `limit` jobs, oldest first.
func (s *Store) LoadJobs(limit int) ([][]byte, error) {
	rows, err := s.db.Query(`SELECT data FROM (SELECT data, created FROM jobs ORDER BY created DESC LIMIT ?) ORDER BY created ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		out = append(out, []byte(data))
	}
	return out, rows.Err()
}

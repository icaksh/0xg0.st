package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type sqliteMetaStore struct {
	db *sql.DB
}

func (s *sqliteMetaStore) Init() error {
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0777); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return err
	}
	s.db = db
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			id TEXT PRIMARY KEY,
			expires_at_ms INTEGER NOT NULL,
			token TEXT NOT NULL,
			original_name TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS files_expires_at_ms_idx ON files (expires_at_ms);
	`)
	return err
}

func (s *sqliteMetaStore) Save(id string, meta fileMeta) error {
	_, err := s.db.Exec(`
		INSERT INTO files (id, expires_at_ms, token, original_name, created_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			expires_at_ms=excluded.expires_at_ms,
			token=excluded.token,
			original_name=excluded.original_name,
			created_at_ms=excluded.created_at_ms
	`, id, meta.ExpiresAtMs, meta.Token, meta.OriginalName, meta.CreatedAtMs)
	return err
}

func (s *sqliteMetaStore) Get(id string) (fileMeta, bool, error) {
	var meta fileMeta
	row := s.db.QueryRow(`
		SELECT expires_at_ms, token, original_name, created_at_ms
		FROM files
		WHERE id = ?
	`, id)
	if err := row.Scan(&meta.ExpiresAtMs, &meta.Token, &meta.OriginalName, &meta.CreatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return fileMeta{}, false, nil
		}
		return fileMeta{}, false, err
	}
	return meta, true, nil
}

func (s *sqliteMetaStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE id = ?`, id)
	return err
}

func (s *sqliteMetaStore) ListExpired(nowMs int64, limit int) ([]string, error) {
	query := `SELECT id FROM files WHERE expires_at_ms > 0 AND expires_at_ms < ?`
	if limit > 0 {
		query += ` LIMIT ?`
	}
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(query, nowMs, limit)
	} else {
		rows, err = s.db.Query(query, nowMs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

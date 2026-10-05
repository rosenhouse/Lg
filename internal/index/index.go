// Package index keeps lg.db, an SQLite index derived from data/ alone.
package index

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite"
)

type Index struct {
	db   *sql.DB
	data string
}

func Open(path, data string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Index{db: db, data: data}, nil
}

func (ix *Index) Reconcile(context.Context) error { return nil }

func (ix *Index) Rebuild(context.Context) error { return nil }

func (ix *Index) Close() error { return ix.db.Close() }

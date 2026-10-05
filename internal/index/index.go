// Package index keeps lg.db, an SQLite index derived from data/ alone.
package index

import (
	"context"
	"database/sql"
	"errors"

	_ "modernc.org/sqlite"
)

// Format is meta.format. Open empties a db of any other format.
const Format = 1

type Index struct {
	db   *sql.DB
	data string
}

// Open opens the index at path of the store's data dir. Every transaction
// takes the write lock at its start, and waits up to 10s for another
// process to release it.
func Open(path, data string) (*Index, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ix := &Index{db: db, data: data}
	if err := ix.inTx(context.Background(), createSchema); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return ix, nil
}

func createSchema(tx *sql.Tx) error {
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	_, err := tx.Exec("INSERT INTO meta (format) VALUES (?)", Format)
	return err
}

func (ix *Index) inTx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func (ix *Index) Reconcile(context.Context) error { return nil }

func (ix *Index) Rebuild(context.Context) error { return nil }

func (ix *Index) Close() error { return ix.db.Close() }

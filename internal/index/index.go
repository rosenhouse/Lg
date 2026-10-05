// Package index keeps lg.db, an SQLite index derived from data/ alone.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/rosenhouse/lg/internal/layout"

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
	if err := ix.inTx(context.Background(), migrate); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return ix, nil
}

// migrate drops every table, then creates the schema, unless meta.format is Format.
func migrate(ctx context.Context, tx *sql.Tx) error {
	var format int
	if err := tx.QueryRowContext(ctx, "SELECT format FROM meta").Scan(&format); err == nil && format == Format {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errors.Join(err, rows.Close())
		}
		names = append(names, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE %q", name)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO meta (format) VALUES (?)", Format)
	return err
}

func (ix *Index) inTx(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// Reconcile re-indexes each run whose unit dirs differ from those indexed,
// and drops the rows of runs no longer on disk. It returns the errors of
// the runs whose files it could not read, after indexing the others.
func (ix *Index) Reconcile(ctx context.Context) error {
	var unreadable error
	err := ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) (err error) {
		unreadable, err = ix.reconcile(ctx, tx)
		return err
	})
	return errors.Join(err, unreadable)
}

// Rebuild indexes every run on disk anew, as Reconcile does.
func (ix *Index) Rebuild(ctx context.Context) error {
	var unreadable error
	err := ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) (err error) {
		for _, table := range tables {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return err
			}
		}
		unreadable, err = ix.reconcile(ctx, tx)
		return err
	})
	return errors.Join(err, unreadable)
}

func (ix *Index) Close() error { return ix.db.Close() }

// reconcile gives the errors of IndexRun apart from those that stop it.
func (ix *Index) reconcile(ctx context.Context, tx *sql.Tx) (unreadable, err error) {
	onDisk, err := unitsOnDisk(ix.data)
	if err != nil {
		return nil, err
	}
	indexed, err := indexedUnits(ctx, tx)
	if err != nil {
		return nil, err
	}
	for runDir := range indexed {
		if _, ok := onDisk[runDir]; !ok {
			if err := deleteRun(ctx, tx, runDir); err != nil {
				return nil, err
			}
		}
	}
	in, err := newInserter(ctx, tx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, in.close()) }()
	var failed []error
	for runDir, units := range onDisk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if slices.Equal(units, indexed[runDir]) {
			continue
		}
		rows, err := IndexRun(runDir)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if err := deleteRun(ctx, tx, runDir); err != nil {
			return nil, err
		}
		if err := in.insert(ctx, runDir, rows); err != nil {
			return nil, err
		}
	}
	return errors.Join(failed...), nil
}

// unitsOnDisk gives the sorted unit dirs of each run dir under data.
func unitsOnDisk(data string) (map[string][]string, error) {
	runs := map[string][]string{}
	runDirs, err := dirs(data, "*", "*", "*", "runs", "*", "*")
	if err != nil {
		return nil, err
	}
	for _, runDir := range runDirs {
		units, err := runUnits(runDir)
		if err != nil {
			return nil, err
		}
		if len(units) > 0 {
			runs[runDir] = units
		}
	}
	return runs, nil
}

// dirs gives the dirs below root whose path matches pattern, one element per level.
func dirs(root string, pattern ...string) ([]string, error) {
	found := []string{root}
	for _, p := range pattern {
		var next []string
		for _, dir := range found {
			entries, err := os.ReadDir(dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if ok, _ := filepath.Match(p, e.Name()); ok && e.IsDir() {
					next = append(next, filepath.Join(dir, e.Name()))
				}
			}
		}
		found = next
	}
	return found, nil
}

// runUnits gives the sorted unit dirs of a run: attempts, artifacts and extracted trees.
func runUnits(runDir string) ([]string, error) {
	attempts, err := dirs(runDir, "attempt-*")
	if err != nil {
		return nil, err
	}
	units := slices.DeleteFunc(attempts, func(dir string) bool {
		_, ok := layout.AttemptNumber(filepath.Base(dir))
		return !ok
	})
	artifacts, err := dirs(runDir, "artifacts", "*")
	if err != nil {
		return nil, err
	}
	for _, dir := range artifacts {
		units = append(units, dir)
		extracted := filepath.Join(dir, "extracted")
		if _, err := os.Lstat(extracted); err == nil {
			units = append(units, extracted)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	slices.Sort(units)
	return units, nil
}

// indexedUnits gives the sorted indexed unit dirs of each run dir.
func indexedUnits(ctx context.Context, tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT path FROM units ORDER BY path")
	if err != nil {
		return nil, err
	}
	runs := map[string][]string{}
	for rows.Next() {
		var unit string
		if err := rows.Scan(&unit); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		runDir := runOf(unit)
		runs[runDir] = append(runs[runDir], unit)
	}
	return runs, errors.Join(rows.Err(), rows.Close())
}

// runOf gives the run dir of an attempt, artifact or extracted unit.
func runOf(unit string) string {
	switch {
	case filepath.Base(unit) == "extracted":
		return filepath.Dir(filepath.Dir(filepath.Dir(unit)))
	case filepath.Base(filepath.Dir(unit)) == "artifacts":
		return filepath.Dir(filepath.Dir(unit))
	}
	return filepath.Dir(unit)
}

// deleteRun deletes every row whose path is runDir or below it.
func deleteRun(ctx context.Context, tx *sql.Tx, runDir string) error {
	for _, table := range tables {
		// '0' follows '/', so the range holds exactly the paths below runDir.
		_, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE path = ? OR (path > ? AND path < ?)", runDir, runDir+"/", runDir+"0")
		if err != nil {
			return err
		}
	}
	return nil
}

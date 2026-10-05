// Package index keeps lg.db, an SQLite index derived from data/ alone.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/lock"
)

// Format is meta.format. Open empties a db of any other format.
const Format = 1

const busyTimeout = 10 * time.Second

type Index struct {
	db         *sql.DB
	path, data string
}

// Open opens the index at path over the data dir data. It starts from empty
// over a file SQLite finds is not a db or is corrupt. A transaction takes the
// write lock at its start, and waits up to busyTimeout for another process
// to release it.
func Open(ctx context.Context, path, data string) (*Index, error) {
	// Every opener holds path.lock, so none opens a file while another replaces it.
	l, err := lock.Wait(path+".lock", busyTimeout, clock.Real{}, func(string) {})
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Release() }()
	ix, err := open(ctx, path, data)
	if !hasCode(err, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT) {
		return ix, err
	}
	// SQLite deletes a WAL beside an empty db.
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return open(ctx, path, data)
}

func open(ctx context.Context, path, data string) (*Index, error) {
	// SQLite decodes a file: URI's path, so no character of path starts the query.
	uri := "file:" + (&url.URL{Path: path}).EscapedPath()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_txlock=immediate", uri, busyTimeout.Milliseconds()))
	if err != nil {
		return nil, err
	}
	ix := &Index{db: db, path: path, data: data}
	if err := ix.resetUnlessCurrent(ctx); err != nil {
		return nil, errors.Join(ix.dbError(err), db.Close())
	}
	return ix, nil
}

// resetUnlessCurrent empties a db whose meta.format is not Format. It takes
// the write lock only to do so.
func (ix *Index) resetUnlessCurrent(ctx context.Context) error {
	if err := ix.connect(ctx); err != nil {
		return err
	}
	if isCurrent(ctx, ix.db) {
		return nil
	}
	return ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if isCurrent(ctx, tx) {
			return nil
		}
		return reset(ctx, tx)
	})
}

// connect retries while SQLITE_BUSY, which SQLite gives at once, without
// waiting, when another connection reads a fresh db as it turns it to WAL.
func (ix *Index) connect(ctx context.Context) error {
	clk := clock.Real{}
	deadline := clk.Now().Add(busyTimeout)
	for {
		err := ix.db.PingContext(ctx)
		if !hasCode(err, sqlite3.SQLITE_BUSY) || clk.Now().After(deadline) {
			return err
		}
		<-clk.After(10 * time.Millisecond)
	}
}

// hasCode reports whether err is an SQLite error with one of the primary codes.
func hasCode(err error, codes ...int) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && slices.Contains(codes, sqliteErr.Code()&0xff)
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func isCurrent(ctx context.Context, q querier) bool {
	var format int
	err := q.QueryRowContext(ctx, "SELECT format FROM meta").Scan(&format)
	return err == nil && format == Format
}

// reset drops every table, then creates the schema.
func reset(ctx context.Context, tx *sql.Tx) error {
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
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+quoteIdentifier(name)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO meta (format) VALUES (?)", Format)
	return err
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// inTx returns f's error alone, since SQLite may have rolled back already.
func (ix *Index) inTx(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Reconcile re-indexes each run whose unit dirs differ from those indexed,
// and drops the rows of runs no longer on disk. It returns the errors of
// the runs whose files it could not read, after indexing the others.
func (ix *Index) Reconcile(ctx context.Context) error {
	indexed, err := indexedUnits(ctx, ix.db)
	if err != nil {
		return ix.dbError(err)
	}
	return ix.index(ctx, indexed, false)
}

// Rebuild indexes every run on disk into an empty lg.db at path.
func Rebuild(ctx context.Context, path, data string) error {
	ix, err := Open(ctx, path, data)
	if err != nil {
		return err
	}
	return errors.Join(ix.index(ctx, nil, true), ix.Close())
}

func (ix *Index) Close() error { return ix.db.Close() }

// index reads the rows of each run on disk whose unit dirs differ from
// indexed. Then, holding the write lock, it writes them, into an emptied db
// when fresh. It takes the lock only when the db changes. It returns the
// errors of the runs whose files it could not read, after indexing the others.
func (ix *Index) index(ctx context.Context, indexed map[string][]string, fresh bool) error {
	onDisk, err := unitsOnDisk(ix.data)
	if err != nil {
		return err
	}
	read := map[string]Rows{}
	var unreadable []error
	for _, runDir := range slices.Sorted(maps.Keys(onDisk)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if slices.Equal(onDisk[runDir], indexed[runDir]) {
			continue
		}
		rows, err := IndexRun(runDir)
		if err != nil {
			if _, statErr := os.Lstat(runDir); errors.Is(statErr, fs.ErrNotExist) {
				// Retention evicted the run while IndexRun read it.
				delete(onDisk, runDir)
			} else {
				unreadable = append(unreadable, err)
			}
			continue
		}
		read[runDir] = rows
	}
	unchanged := !fresh && len(read) == 0
	for runDir := range indexed {
		if _, ok := onDisk[runDir]; !ok {
			unchanged = false
		}
	}
	if unchanged {
		return errors.Join(unreadable...)
	}
	err = ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if fresh {
			if err := reset(ctx, tx); err != nil {
				return err
			}
		}
		return write(ctx, tx, onDisk, read)
	})
	return errors.Join(append([]error{ix.dbError(err)}, unreadable...)...)
}

// dbError names lg.db in err.
func (ix *Index) dbError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", ix.path, err)
}

// write drops the rows of indexed runs not on disk, and replaces those of each read run.
func write(ctx context.Context, tx *sql.Tx, onDisk map[string][]string, read map[string]Rows) (err error) {
	indexed, err := indexedUnits(ctx, tx)
	if err != nil {
		return err
	}
	for runDir := range indexed {
		if _, ok := onDisk[runDir]; !ok {
			if err := deleteRun(ctx, tx, runDir); err != nil {
				return err
			}
		}
	}
	in, err := newInserter(ctx, tx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.close()) }()
	for runDir, rows := range read {
		if err := deleteRun(ctx, tx, runDir); err != nil {
			return err
		}
		if err := in.insert(ctx, runDir, rows); err != nil {
			return err
		}
	}
	return nil
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
func indexedUnits(ctx context.Context, q querier) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT path FROM units ORDER BY path")
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

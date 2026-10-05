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

// busyTimeout is how long a transaction waits for another's write lock.
var busyTimeout = 10 * time.Second

// lockTimeout is how long an index user waits for lg.db.lock, which a
// writer holds while it indexes.
const lockTimeout = 5 * time.Minute

type Index struct {
	db         *sql.DB
	path, data string
}

// Open opens the index at path over the data dir data. It starts from empty
// when SQLite cannot read meta. A transaction takes the write lock at its
// start, and waits up to busyTimeout for another process to release it.
func Open(ctx context.Context, path, data string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l, err := lockFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Release() }()
	return openReadable(ctx, path, data)
}

// lockFile takes path.lock. Every opener and every writer holds it, so none
// opens a file while another replaces it, and writers take turns.
func lockFile(path string) (*lock.Lock, error) {
	return lock.Wait(path+".lock", lockTimeout, clock.Real{}, func(string) {})
}

// openReadable opens path, first removing a file SQLite cannot read.
func openReadable(ctx context.Context, path, data string) (*Index, error) {
	ix, err := open(ctx, path, data)
	if !unreadable(err) {
		return ix, err
	}
	// SQLite deletes a WAL beside an empty db.
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return open(ctx, path, data)
}

func unreadable(err error) bool { return hasCode(err, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT) }

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
	return ix.orStartOver(ctx, func() error {
		changed, err := ix.changed(ctx)
		if err != nil || !changed {
			return err
		}
		return ix.index(ctx, false)
	})
}

// changed reports whether the units on disk differ from those indexed.
func (ix *Index) changed(ctx context.Context) (bool, error) {
	indexed, err := indexedUnits(ctx, ix.db)
	if err != nil {
		return false, ix.dbError(err)
	}
	onDisk, err := unitsOnDisk(ix.data)
	if err != nil {
		return false, err
	}
	return !maps.EqualFunc(onDisk, indexed, slices.Equal[[]string]), nil
}

// Rebuild indexes every run on disk into an empty lg.db at path.
func Rebuild(ctx context.Context, path, data string) error {
	ix, err := Open(ctx, path, data)
	if err != nil {
		return err
	}
	return errors.Join(ix.orStartOver(ctx, func() error { return ix.index(ctx, true) }), ix.Close())
}

// orStartOver runs f. If SQLite cannot read lg.db, it then indexes every run
// into an empty db.
func (ix *Index) orStartOver(ctx context.Context, f func() error) error {
	err := f()
	if !unreadable(err) {
		return err
	}
	if err := ix.startOver(ctx); err != nil {
		return err
	}
	return ix.index(ctx, true)
}

// startOver replaces lg.db, which SQLite cannot read, with an empty db,
// unless another has replaced it meanwhile.
func (ix *Index) startOver(ctx context.Context) error {
	unread, statErr := os.Stat(ix.path)
	// The unreadable db is about to go.
	_ = ix.db.Close()
	l, err := lockFile(ix.path)
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()
	if now, err := os.Stat(ix.path); err == nil && statErr == nil && os.SameFile(unread, now) {
		if err := os.Remove(ix.path); err != nil {
			return err
		}
	}
	replaced, err := openReadable(ctx, ix.path, ix.data)
	if err != nil {
		return err
	}
	ix.db = replaced.db
	return nil
}

func (ix *Index) Close() error { return ix.db.Close() }

// index holds lg.db.lock while it reads the rows of each run on disk whose
// unit dirs differ from those indexed, or of every run when fresh. Then,
// holding the write lock, it writes them, into an emptied db when fresh. It
// takes the write lock only when the db changes. It returns the errors of the
// runs whose files it could not read, after indexing the others.
func (ix *Index) index(ctx context.Context, fresh bool) error {
	l, err := lockFile(ix.path)
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()
	var indexed map[string][]string
	if !fresh {
		if indexed, err = indexedUnits(ctx, ix.db); err != nil {
			return ix.dbError(err)
		}
	}
	onDisk, err := unitsOnDisk(ix.data)
	if err != nil {
		return err
	}
	read := map[string]Rows{}
	var unreadable []error
	for _, runDir := range slices.Sorted(maps.Keys(onDisk)) {
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

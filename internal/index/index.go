// Package index keeps lg.db, an SQLite index derived from data/ alone.
package index

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// Format is meta.format. Open empties a db of any other format or schema.
const Format = 1

// busyTimeout is how long a transaction waits for another's write lock.
var busyTimeout = 10 * time.Second

// lockTimeout is how long an index user waits for lg.db.lock, which a
// writer holds while it indexes.
const lockTimeout = 5 * time.Minute

// quietWait is how long an index user waits for lg.db.lock before saying so.
const quietWait = time.Second

// Index is lg.db, or an index held in memory alone when path is "".
type Index struct {
	db         *sql.DB
	path, data string
	waiting    func(holder string)
}

// DBError is an error of lg.db or lg.db.lock, rather than of data/.
type DBError struct{ Err error }

func (e *DBError) Error() string { return e.Err.Error() }

func (e *DBError) Unwrap() error { return e.Err }

func ofDB(err error) error {
	if err == nil {
		return nil
	}
	return &DBError{err}
}

// Open opens the index at path over the data dir data. It starts from empty
// when lg.db is unreadable or has another format or schema. A transaction
// takes the write lock at its start, and waits up to busyTimeout for another
// process to release it.
// Whenever the index waits longer than quietWait for lg.db.lock, it calls
// waiting, unless nil, with the holder.
func Open(ctx context.Context, path, data string, waiting func(holder string)) (*Index, error) {
	if waiting == nil {
		waiting = func(string) {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, ofDB(err)
	}
	ix := &Index{path: path, data: data, waiting: waiting}
	release, err := ix.lockFile(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := ix.openReadable(ctx); err != nil {
		return nil, ofDB(err)
	}
	return ix, nil
}

// OpenMemory opens an empty index over the data dir data, held in memory
// alone, for when lg.db is unusable.
func OpenMemory(ctx context.Context, data string) (*Index, error) {
	ix := &Index{data: data, waiting: func(string) {}}
	return ix, ix.open(ctx)
}

// lockFile takes lg.db.lock, unless ctx is done first. Every opener and
// every writer holds it, so none opens a file while another replaces it, and
// writers take turns. An index in memory takes none.
func (ix *Index) lockFile(ctx context.Context) (release func(), err error) {
	if ix.path == "" {
		return func() {}, nil
	}
	l, err := lock.WaitContext(ctx, ix.path+".lock", quietWait, clock.Real{}, func(string) {})
	if errors.Is(err, lock.ErrTimeout) {
		l, err = lock.WaitContext(ctx, ix.path+".lock", lockTimeout-quietWait, clock.Real{}, ix.waiting)
	}
	if err != nil {
		return nil, ofDB(err)
	}
	return func() { _ = l.Release() }, nil
}

// openReadable opens lg.db, first removing a file SQLite cannot read.
func (ix *Index) openReadable(ctx context.Context) error {
	err := ix.open(ctx)
	if !unreadable(err) {
		return err
	}
	// SQLite deletes a WAL beside an empty db.
	if err := os.Remove(ix.path); err != nil {
		return err
	}
	return ix.open(ctx)
}

func unreadable(err error) bool { return hasCode(err, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT) }

func (ix *Index) open(ctx context.Context) error {
	// SQLite decodes a file: URI's path, so no character of path starts the query.
	uri := "file:" + (&url.URL{Path: ix.path}).EscapedPath()
	if ix.path == "" {
		uri = "file::memory:"
	}
	// temp_store(2) keeps sorts from spilling to $TMPDIR, which may be full while data/ is readable.
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=temp_store(2)&_txlock=immediate", uri, busyTimeout.Milliseconds()))
	if err != nil {
		return err
	}
	if ix.path == "" {
		// Each connection to :memory: has a db of its own.
		db.SetMaxOpenConns(1)
	}
	ix.db = db
	if err := ix.resetUnlessCurrent(ctx); err != nil {
		return errors.Join(ix.dbError(err), db.Close())
	}
	return nil
}

// resetUnlessCurrent empties a db that isCurrent rejects. It takes
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

// isCurrent reports whether the db has format Format and this schema.
func isCurrent(ctx context.Context, q querier) bool {
	var format int
	var sum string
	err := q.QueryRowContext(ctx, "SELECT format, schema FROM meta").Scan(&format, &sum)
	return err == nil && format == Format && sum == schemaSum()
}

func schemaSum() string {
	sum := sha256.Sum256([]byte(schema))
	return hex.EncodeToString(sum[:])
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
	_, err = tx.ExecContext(ctx, "INSERT INTO meta (format, schema) VALUES (?, ?)", Format, schemaSum())
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
// the units whose files it could not read, after indexing the others.
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
	return !maps.EqualFunc(onDisk, indexed, slices.Equal[[]UnitDir]), nil
}

// Rebuild indexes every run on disk into an empty lg.db at path.
func Rebuild(ctx context.Context, path, data string, waiting func(holder string)) error {
	ix, err := Open(ctx, path, data, waiting)
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
	release, err := ix.lockFile(ctx)
	if err != nil {
		return err
	}
	defer release()
	if now, err := os.Stat(ix.path); err == nil && statErr == nil && os.SameFile(unread, now) {
		if err := os.Remove(ix.path); err != nil {
			return ofDB(err)
		}
	}
	return ofDB(ix.openReadable(ctx))
}

func (ix *Index) Close() error { return ix.db.Close() }

// index holds lg.db.lock while it reads the rows of each run on disk whose
// unit dirs differ from those indexed, or of every run when fresh. Then,
// holding the write lock, it writes them, into an emptied db when fresh. It
// takes the write lock only when the db changes. It returns the errors of the
// units whose files it could not read, after indexing the others.
func (ix *Index) index(ctx context.Context, fresh bool) error {
	release, err := ix.lockFile(ctx)
	if err != nil {
		return err
	}
	defer release()
	var indexed map[string][]UnitDir
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
	var failed []error
	for _, runDir := range slices.Sorted(maps.Keys(onDisk)) {
		if slices.Equal(onDisk[runDir], indexed[runDir]) {
			continue
		}
		rows, err := IndexRun(runDir)
		if err != nil {
			if _, statErr := os.Lstat(runDir); errors.Is(statErr, fs.ErrNotExist) {
				// Retention evicted the run while IndexRun read it.
				delete(onDisk, runDir)
				continue
			}
			failed = append(failed, err)
		}
		if len(rows.Units) > 0 {
			read[runDir] = rows
		}
	}
	unchanged := !fresh && len(read) == 0
	for runDir := range indexed {
		if _, ok := onDisk[runDir]; !ok {
			unchanged = false
		}
	}
	if unchanged {
		return errors.Join(failed...)
	}
	err = ix.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if fresh {
			if err := reset(ctx, tx); err != nil {
				return err
			}
		}
		return write(ctx, tx, onDisk, read)
	})
	return errors.Join(append([]error{ix.dbError(err)}, failed...)...)
}

// dbError names lg.db in err.
func (ix *Index) dbError(err error) error {
	if err == nil {
		return nil
	}
	return ofDB(fmt.Errorf("%s: %w", cmp.Or(ix.path, "the index in memory"), err))
}

// write drops the rows of indexed runs not on disk, and replaces those of each read run.
func write(ctx context.Context, tx *sql.Tx, onDisk map[string][]UnitDir, read map[string]Rows) (err error) {
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

// unitsOnDisk gives the sorted units of each run dir under data.
func unitsOnDisk(data string) (map[string][]UnitDir, error) {
	runs := map[string][]UnitDir{}
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

// runUnits gives the sorted units of a run: attempts, artifacts and extracted trees.
func runUnits(runDir string) ([]UnitDir, error) {
	attempts, err := dirs(runDir, "attempt-*")
	if err != nil {
		return nil, err
	}
	paths := slices.DeleteFunc(attempts, func(dir string) bool {
		_, ok := layout.AttemptNumber(filepath.Base(dir))
		return !ok
	})
	artifacts, err := dirs(runDir, "artifacts", "*")
	if err != nil {
		return nil, err
	}
	for _, dir := range artifacts {
		paths = append(paths, dir, filepath.Join(dir, "extracted"))
	}
	slices.Sort(paths)
	var units []UnitDir
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		units = append(units, UnitDir{Path: path, Modified: info.ModTime().UnixNano()})
	}
	return units, nil
}

// indexedUnits gives the sorted indexed units of each run dir.
func indexedUnits(ctx context.Context, q querier) (map[string][]UnitDir, error) {
	rows, err := q.QueryContext(ctx, "SELECT path, modified FROM units ORDER BY path")
	if err != nil {
		return nil, err
	}
	runs := map[string][]UnitDir{}
	for rows.Next() {
		var unit UnitDir
		if err := rows.Scan(&unit.Path, &unit.Modified); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		runDir := runOf(unit.Path)
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

package index_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	_ "modernc.org/sqlite"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

const (
	runID      = 37129390741
	deletedRun = 37129738159
	cloneID    = 1
)

var syncTimeout = NodeTimeout(30 * time.Second)

// waitTimeout outlasts the busy timeout an index user may spend waiting for the write lock.
const waitTimeout = 15 * time.Second

var tables = []string{"runs", "attempts", "jobs", "steps", "artifacts", "tombstones", "units"}

// openDB opens lg.db as any SQLite client would.
func openDB(path string) *sql.DB {
	GinkgoHelper()
	db, err := sql.Open("sqlite", path)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(db.Close)
	return db
}

func dbPath(env *harness.InProcessEnv) string { return filepath.Join(env.State(), "lg.db") }

// reconcile opens the index at path over data, reconciles it and closes it.
func reconcile(ctx context.Context, path string, data string) {
	GinkgoHelper()
	ix, err := index.Open(ctx, path, data)
	Expect(err).NotTo(HaveOccurred())
	Expect(ix.Reconcile(ctx)).To(Succeed())
	Expect(ix.Close()).To(Succeed())
}

// syncStages syncs the fixture run through each stage in turn.
func syncStages(ctx context.Context, env *harness.InProcessEnv, stages ...string) {
	GinkgoHelper()
	Expect(env.Fake.Load(runID, stages[0])).To(Succeed())
	Expect(env.Sync(ctx)).To(Succeed())
	for _, stage := range stages[1:] {
		Expect(env.Fake.Advance(runID, stage)).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
	}
}

func count(db *sql.DB, query string, args ...any) int {
	GinkgoHelper()
	var n int
	Expect(db.QueryRowContext(context.Background(), query, args...).Scan(&n)).To(Succeed())
	return n
}

// column gives the first column of each row.
func column[T any](db *sql.DB, query string, args ...any) []T {
	GinkgoHelper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(rows.Close)
	var values []T
	for rows.Next() {
		var v T
		Expect(rows.Scan(&v)).To(Succeed())
		values = append(values, v)
	}
	Expect(rows.Err()).To(Succeed())
	return values
}

// row gives one row as column name to value.
func row(db *sql.DB, query string, args ...any) map[string]any {
	GinkgoHelper()
	all := dump(db, query, args...)
	Expect(all).To(HaveLen(1))
	return all[0]
}

func dump(db *sql.DB, query string, args ...any) []map[string]any {
	GinkgoHelper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(rows.Close)
	names, err := rows.Columns()
	Expect(err).NotTo(HaveOccurred())
	var all []map[string]any
	for rows.Next() {
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		Expect(rows.Scan(pointers...)).To(Succeed())
		m := map[string]any{}
		for i, name := range names {
			m[name] = values[i]
		}
		all = append(all, m)
	}
	Expect(rows.Err()).To(Succeed())
	return all
}

// contents lists every row of every table, with paths relative to data, sorted.
func contents(db *sql.DB, data string) map[string][]string {
	GinkgoHelper()
	all := map[string][]string{}
	for _, table := range tables {
		var lines []string
		for _, r := range dump(db, "SELECT * FROM "+table) {
			if p, ok := r["path"].(string); ok {
				r["path"] = strings.TrimPrefix(p, data)
			}
			lines = append(lines, fmt.Sprint(r))
		}
		slices.Sort(lines)
		all[table] = lines
	}
	return all
}

// runDir is the only dir of the run under data/.
func runDir(data string, id int64) string {
	GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(data, "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", id)))
	Expect(err).NotTo(HaveOccurred())
	Expect(dirs).To(HaveLen(1))
	return dirs[0]
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

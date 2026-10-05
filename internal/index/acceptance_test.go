package index_test

import (
	"database/sql"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("the index after syncing through after-attempt-1, -2 and -3", Label("index"), func() {
	var (
		env *harness.InProcessEnv
		db  *sql.DB
	)

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		syncStages(ctx, env, "after-attempt-1", "after-attempt-2", "after-attempt-3")
		reconcile(ctx, dbPath(env), env.Data())
		db = openDB(dbPath(env))
	}, syncTimeout)

	It("holds 1 run, 3 attempts, 36 jobs (8 carried_forward with their original job ids, 4 not_applicable), their steps, 12 artifacts and the tombstone reasons, with a path on every row", func() {
		Expect(count(db, "SELECT count(*) FROM runs")).To(Equal(1))
		Expect(count(db, "SELECT count(*) FROM attempts")).To(Equal(3))
		Expect(count(db, "SELECT count(*) FROM jobs")).To(Equal(36))
		Expect(column[string](db, "SELECT kind || ' ' || count(*) FROM jobs GROUP BY kind")).To(ConsistOf(
			"ran 24", "carried_forward 8", "not_applicable 4"))
		Expect(count(db, `SELECT count(*) FROM jobs j JOIN jobs o ON o.job_id = j.original_job_id
			WHERE j.kind = 'carried_forward' AND o.kind = 'ran' AND o.name = j.name AND o.attempt < j.attempt`)).To(Equal(8))
		Expect(count(db, "SELECT count(*) FROM jobs WHERE kind != 'carried_forward' AND original_job_id IS NOT NULL")).To(Equal(0))

		steps := 0
		for attempt := 1; attempt <= 3; attempt++ {
			jobs, err := recordings.Jobs(runID, "after-attempt-3", attempt)
			Expect(err).NotTo(HaveOccurred())
			for _, job := range jobs {
				steps += len(job.Steps)
			}
		}
		Expect(count(db, "SELECT count(*) FROM steps")).To(Equal(steps))

		Expect(count(db, "SELECT count(*) FROM artifacts WHERE has_zip")).To(Equal(12))
		Expect(column[string](db, "SELECT reason || ' ' || count(*) FROM tombstones GROUP BY reason")).To(ConsistOf("not_applicable 4"))

		for _, table := range tables {
			paths := column[sql.NullString](db, "SELECT path FROM "+table)
			Expect(paths).NotTo(BeEmpty(), table)
			for _, p := range paths {
				Expect(p.Valid && exists(p.String)).To(BeTrue(), "%s row with path %q", table, p.String)
			}
		}
	})

	It("holds the recorded values of attempt 1, its failed job flaky, and that job's failed step and the step before", func() {
		attempt := layout.AttemptDir(runDir(env.Data(), runID), 1)
		Expect(row(db, "SELECT * FROM attempts WHERE attempt = 1")).To(Equal(map[string]any{
			"run_id": int64(runID), "attempt": int64(1), "path": attempt, "status": "completed", "conclusion": "failure",
			"run_started_at": "2026-10-03T14:22:54Z", "completed_at": "2026-10-03T14:24:12Z",
		}))
		job := layout.JobDir(attempt, 111221289888, "flaky")
		log, err := os.Stat(filepath.Join(job, "log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(row(db, "SELECT * FROM jobs WHERE job_id = 111221289888")).To(Equal(map[string]any{
			"job_id": int64(111221289888), "run_id": int64(runID), "attempt": int64(1), "name": "flaky", "slug": "flaky",
			"kind": "ran", "original_job_id": nil, "conclusion": "failure",
			"started_at": "2026-10-03T14:22:57Z", "completed_at": "2026-10-03T14:23:03Z",
			"runner_name": "GitHub Actions 1000002382", "labels": `["ubuntu-latest"]`,
			"has_log": int64(1), "log_bytes": log.Size(), "path": job,
		}))
		Expect(dump(db, "SELECT * FROM steps WHERE job_id = 111221289888 AND number IN (4, 7) ORDER BY number")).To(Equal([]map[string]any{{
			"job_id": int64(111221289888), "number": int64(4), "name": "Upload same-named artifact with overwrite", "conclusion": "success",
			"started_at": "2026-10-03T14:22:59Z", "completed_at": "2026-10-03T14:23:01Z", "path": job,
		}, {
			"job_id": int64(111221289888), "number": int64(7), "name": "Fail on first attempt only", "conclusion": "failure",
			"started_at": "2026-10-03T14:23:01Z", "completed_at": "2026-10-03T14:23:01Z", "path": job,
		}}))
	})

	It("attributes 11276267449, 11276052917 and 11275918123 to attempt 2 by listing-diff, and the 4 artifacts first seen with attempt 1 to attempt 1", func() {
		attributed := func(id int64) []string {
			return column[string](db, "SELECT attributed_attempt || ' ' || attribution FROM artifacts WHERE artifact_id = ?", id)
		}
		for _, id := range []int64{11276267449, 11276052917, 11275918123} {
			Expect(attributed(id)).To(Equal([]string{"2 listing-diff"}), "artifact %d", id)
		}
		for _, id := range []int64{11276401837, 11276272069, 11276182467, 11275917910} {
			Expect(attributed(id)).To(ConsistOf(HavePrefix("1 ")), "artifact %d", id)
		}
	})
})

var _ = Describe("the index after a first sync at after-attempt-3", Label("index"), func() {
	It("attributes all 5 listed artifacts to attempt 3 by timestamp", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-3")
		reconcile(ctx, dbPath(env), env.Data())

		listed, err := recordings.Artifacts(runID, "after-attempt-3")
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(5))
		db := openDB(dbPath(env))
		for _, a := range listed {
			Expect(column[string](db, "SELECT attributed_attempt || ' ' || attribution FROM artifacts WHERE artifact_id = ?", a.ID)).
				To(Equal([]string{"3 timestamp"}), "artifact %d", a.ID)
		}
	}, syncTimeout)
})

var _ = Describe("index.Reconcile", Label("index"), func() {
	It("adds units published since the last reconcile and drops the rows of evicted runs", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())

		reconcile(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(ConsistOf(int64(runID), int64(deletedRun)))
		Expect(count(db, "SELECT count(*) FROM jobs WHERE run_id = ?", deletedRun)).To(BeNumerically(">", 0))

		evicted := runDir(env.Data(), runID)
		Expect(os.RemoveAll(evicted)).To(Succeed())
		reconcile(ctx, dbPath(env), env.Data())
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(ConsistOf(int64(deletedRun)))
		for _, table := range tables {
			Expect(count(db, "SELECT count(*) FROM "+table+" WHERE path = ? OR path LIKE ?", evicted, evicted+"/%")).
				To(Equal(0), table)
		}
		Expect(count(db, "SELECT count(*) FROM jobs")).To(BeNumerically(">", 0))
	}, syncTimeout)

	It("re-indexes a run whose artifact gains extracted/, and leaves the rows of unchanged runs alone", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		tamper := func() {
			_, err := db.ExecContext(ctx, "UPDATE runs SET workflow_name = 'tampered'")
			Expect(err).NotTo(HaveOccurred())
		}
		workflow := func() []string { return column[string](db, "SELECT workflow_name FROM runs") }

		tamper()
		reconcile(ctx, dbPath(env), env.Data())
		Expect(workflow()).To(Equal([]string{"tampered"}))

		Expect(os.Mkdir(filepath.Join(env.ArtifactDirs(runID)[0], "extracted"), 0o755)).To(Succeed())
		reconcile(ctx, dbPath(env), env.Data())
		Expect(count(db, "SELECT count(*) FROM artifacts WHERE extracted")).To(Equal(1))
		Expect(workflow()).To(Equal([]string{"lg-fixture"}))

		tamper()
		reconcile(ctx, dbPath(env), env.Data())
		Expect(workflow()).To(Equal([]string{"tampered"}))
	}, syncTimeout)

	It("gives the same rows whichever attempt of a run is indexed first, with created_at from fetch.json run_created_at", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-2")
		dir := runDir(env.Data(), runID)

		indexedFirst := func(attempt int) map[string][]string {
			path := filepath.Join(GinkgoT().TempDir(), "lg.db")
			other := layout.AttemptDir(dir, 3-attempt)
			hidden := filepath.Join(env.Tmp(), "hidden")
			Expect(os.Rename(other, hidden)).To(Succeed())
			reconcile(ctx, path, env.Data())
			Expect(os.Rename(hidden, other)).To(Succeed())
			reconcile(ctx, path, env.Data())
			return contents(openDB(path), env.Data())
		}
		first1 := indexedFirst(1)
		Expect(first1["attempts"]).To(HaveLen(2))
		Expect(indexedFirst(2)).To(Equal(first1))

		path := filepath.Join(GinkgoT().TempDir(), "lg.db")
		reconcile(ctx, path, env.Data())
		Expect(column[string](openDB(path), "SELECT created_at FROM runs")).To(Equal([]string{"2026-10-03T14:22:54Z"}))
	}, syncTimeout)

	It("indexes an artifact whose attempt is not yet on disk from artifact.json and its fetch.json, with the run's workflow, event and PR numbers", func(ctx SpecContext) {
		env := harness.InProcess()
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID)
		Expect(env.Fake.AddRun(scenario.WithPullRequests(scenario.InProgress(run, 1), 42, 7))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(cloneID)).To(BeEmpty())

		reconcile(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		Expect(row(db, "SELECT * FROM runs")).To(SatisfyAll(
			HaveKeyWithValue("run_id", BeEquivalentTo(cloneID)),
			HaveKeyWithValue("host", "github.com"),
			HaveKeyWithValue("repo", "rosenhouse/Lg"),
			HaveKeyWithValue("created_at", "2026-10-03T14:22:54Z"),
			HaveKeyWithValue("date_dir", "2026-10-03"),
			HaveKeyWithValue("path", runDir(env.Data(), cloneID)),
			HaveKeyWithValue("workflow_id", BeEquivalentTo(373958224)),
			HaveKeyWithValue("workflow_name", "lg-fixture"),
			HaveKeyWithValue("head_branch", "lg-fixture"),
			HaveKeyWithValue("head_sha", "1a51097dadb5b55978ac401b93f1ca9d8d317b02"),
			HaveKeyWithValue("event", "push"),
			HaveKeyWithValue("pr_numbers", "[7,42]"),
			HaveKeyWithValue("display_title", "Add lg-fixture workflow for recording Actions API shapes"),
			HaveKeyWithValue("latest_attempt", BeNil()),
		))
		Expect(column[string](db, "SELECT attribution FROM artifacts WHERE run_id = ? AND has_zip", cloneID)).
			To(Equal([]string{"unknown", "unknown", "unknown", "unknown"}))
	}, syncTimeout)
})

var _ = Describe("index.Open", Label("index"), func() {
	It("drops and rebuilds lg.db when meta.format differs", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		_, err := db.Exec("UPDATE meta SET format = 0; INSERT INTO runs (run_id, path) VALUES (99, 'stale')")
		Expect(err).NotTo(HaveOccurred())

		ix, err := index.Open(dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(column[int](db, "SELECT format FROM meta")).To(Equal([]int{1}))
		Expect(count(db, "SELECT count(*) FROM runs")).To(Equal(0))

		Expect(ix.Reconcile(ctx)).To(Succeed())
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(Equal([]int64{runID}))
		Expect(count(db, "SELECT count(*) FROM jobs")).To(Equal(12))
	}, syncTimeout)
})

package index_test

import (
	"cmp"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const (
	// pendingFork is an artifact-only run from a fork's main.
	pendingFork = 9
	// sameTimeLow and sameTimeHigh were created at the same time; their
	// dirs sort the other way round from their ids.
	sameTimeLow, sameTimeHigh = 20, 100
	// renamed is a run of the Archaeology workflow under the name "renamed", with SHA aaaa….
	renamed = 30
)

// archaeology syncs the Archaeology runs and the runs above, writes an
// extracted/ tree into the first artifact of Release3 and into every
// artifact of Rerun, and gives the index of them.
func archaeology(ctx context.Context) (*harness.InProcessEnv, *index.Index) {
	GinkgoHelper()
	env := harness.InProcess()
	env.Mirror.Backfill = 60 * scenario.Day
	sameTime := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	runs := append(scenario.Archaeology().All(),
		scenario.OnBranch(scenario.FromFork(scenario.InProgress(scenario.CloneAt(pendingFork, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), 1), "someone/Lg"), "main"),
		scenario.CloneAt(sameTimeLow, "after-attempt-1", sameTime),
		scenario.CloneAt(sameTimeHigh, "after-attempt-1", sameTime),
		scenario.RenameWorkflow(scenario.WithSHA(scenario.CloneAt(renamed, "after-attempt-1", sameTime), strings.Repeat("a", 40)), 1, "renamed"))
	for _, r := range runs {
		Expect(env.Fake.AddRun(r)).To(Succeed())
	}
	Expect(env.Sync(ctx)).To(Succeed())
	writeExtracted(artifactDirs(env, scenario.Archaeology().Release3.ID)[0])
	for _, dir := range artifactDirs(env, scenario.Archaeology().Rerun.ID) {
		writeExtracted(dir)
	}
	ix, err := index.Open(ctx, dbPath(env), env.Data(), nil)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(ix.Close)
	Expect(ix.Reconcile(ctx)).To(Succeed())
	return env, ix
}

// writeExtracted writes a tree that lg extract could have written: files,
// an empty dir, a symlink, and .lg-extract.json.
func writeExtracted(artifactDir string) {
	GinkgoHelper()
	extracted := filepath.Join(artifactDir, "extracted")
	Expect(os.MkdirAll(filepath.Join(extracted, "report.zip.d", "logs"), 0o755)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(extracted, "empty"), 0o755)).To(Succeed())
	for _, name := range []string{"summary.txt", "report.zip.d/logs/test.log", ".lg-extract.json", "report.zip.d/.lg-extract.json"} {
		Expect(os.WriteFile(filepath.Join(extracted, name), []byte("foo bar\n"), 0o644)).To(Succeed())
	}
	Expect(os.Symlink("summary.txt", filepath.Join(extracted, "link.txt"))).To(Succeed())
}

func artifactDirs(env *harness.InProcessEnv, id int64) []string {
	GinkgoHelper()
	dirs := env.ArtifactDirs(id)
	Expect(dirs).NotTo(BeEmpty())
	return dirs
}

// under gives the paths matching pattern below the dir of each run.
func under(env *harness.InProcessEnv, pattern string, ids ...int64) []string {
	GinkgoHelper()
	var all []string
	for _, id := range ids {
		found, err := filepath.Glob(filepath.Join(runDir(env.Data(), id), pattern))
		Expect(err).NotTo(HaveOccurred())
		all = append(all, found...)
	}
	return all
}

func logsOf(env *harness.InProcessEnv, ids ...int64) []string {
	return under(env, "attempt-*/jobs/*/log.txt", ids...)
}

// extractedFiles gives the regular files below the extracted/ trees of the
// runs, without .lg-extract.json at the top of each.
func extractedFiles(env *harness.InProcessEnv, ids ...int64) []string {
	GinkgoHelper()
	var files []string
	for _, dir := range under(env, "artifacts/*/extracted", ids...) {
		Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && path != filepath.Join(dir, ".lg-extract.json") {
				files = append(files, path)
			}
			return err
		})).To(Succeed())
	}
	return files
}

// createdSince keeps the paths below the artifact dirs created at or after t.
func createdSince(paths []string, t time.Time) []string {
	GinkgoHelper()
	return slices.DeleteFunc(slices.Clone(paths), func(path string) bool {
		dir := path
		for filepath.Base(filepath.Dir(dir)) != "artifacts" {
			dir = filepath.Dir(dir)
		}
		var artifact struct {
			CreatedAt time.Time `json:"created_at"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, "artifact.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &artifact)).To(Succeed())
		return artifact.CreatedAt.Before(t)
	})
}

var _ = Describe("Index.Paths", Label("paths"), Ordered, ContinueOnFailure, func() {
	var (
		env *harness.InProcessEnv
		ix  *index.Index
		a   = scenario.Archaeology()
	)

	BeforeAll(func(ctx SpecContext) {
		env, ix = archaeology(ctx)
	}, NodeTimeout(time.Minute))

	paths := func(f index.Filter, u index.Unit) []string {
		GinkgoHelper()
		got, err := ix.Paths(context.Background(), f, u)
		Expect(err).NotTo(HaveOccurred())
		return got
	}

	day := func(s string) time.Time {
		t, err := time.Parse(time.DateOnly, s)
		Expect(err).NotTo(HaveOccurred())
		return t
	}

	DescribeTable("filters",
		func(f index.Filter, want func() []string) {
			expected := want()
			Expect(paths(f, index.UnitLog)).To(ConsistOf(expected))
		},
		Entry("a repeated --branch is OR, and matches no run from a fork", index.Filter{Branches: []string{"main", "release-3"}}, func() []string {
			return logsOf(env, a.MainAugust.ID, a.MainSeptember.ID, a.Release3.ID, a.TitleOnly.ID, a.Rerun.ID)
		}),
		Entry("filters combine with AND", index.Filter{Branches: []string{"main"}, SHAs: []string{"2"}}, func() []string {
			return logsOf(env, a.MainSeptember.ID)
		}),
		Entry("a sha prefix uses LIKE", index.Filter{SHAs: []string{"222", "33"}}, func() []string {
			return logsOf(env, a.MainSeptember.ID, a.Release3.ID)
		}),
		Entry("a sha holding LIKE's wildcards matches them literally", index.Filter{SHAs: []string{"%", "_"}}, func() []string { return nil }),
		Entry("a branch is matched exactly", index.Filter{Branches: []string{"mai"}}, func() []string { return nil }),
		Entry("--job uses GLOB", index.Filter{Jobs: []string{"matrix*", "fla?y"}, SHAs: []string{"3"}}, func() []string {
			return slices.Concat(under(env, "attempt-*/jobs/*_matrix-*/log.txt", a.Release3.ID), under(env, "attempt-*/jobs/*_flaky/log.txt", a.Release3.ID))
		}),
		Entry("--job is case-sensitive", index.Filter{Jobs: []string{"Matrix*"}}, func() []string { return nil }),
		Entry("--pr matches any of the run's pull requests", index.Filter{PRs: []int{41, 42}}, func() []string { return logsOf(env, a.PR42.ID) }),
		Entry("--workflow", index.Filter{Workflows: []string{"ci"}}, func() []string { return nil }),
		Entry("--workflow matches the runs of a workflow that has the name", index.Filter{Workflows: []string{"renamed"}, SHAs: []string{"3"}}, func() []string {
			return logsOf(env, a.Release3.ID)
		}),
		Entry("--workflow matches the runs of a workflow that had the name", index.Filter{Workflows: []string{"lg-fixture"}, SHAs: []string{"a"}}, func() []string {
			return logsOf(env, renamed)
		}),
		Entry("--event", index.Filter{Events: []string{"pull_request"}}, func() []string { return logsOf(env, a.Fork.ID, a.PR42.ID) }),
		Entry("--conclusion of a job", index.Filter{Conclusions: []string{"failure"}, SHAs: []string{"8"}}, func() []string {
			return under(env, "attempt-1/jobs/*_flaky/log.txt", a.Rerun.ID)
		}),
	)

	DescribeTable("each unit filters on its own time",
		func(u index.Unit, want func() []string) {
			expected := want()
			Expect(paths(index.Filter{SHAs: []string{"8"}, Since: day("2026-09-15")}, u)).To(ConsistOf(expected))
		},
		Entry("run: the run's created_at", index.UnitRun, func() []string { return nil }),
		Entry("attempt: the attempt's run_started_at", index.UnitAttempt, func() []string {
			return under(env, "attempt-2/*.json", a.Rerun.ID)
		}),
		Entry("job: the attempt's run_started_at", index.UnitJob, func() []string {
			return under(env, "attempt-2/jobs/*/job.json", a.Rerun.ID)
		}),
		Entry("log: the attempt's run_started_at", index.UnitLog, func() []string {
			return under(env, "attempt-2/jobs/*/log.txt", a.Rerun.ID)
		}),
		Entry("artifact: the artifact's created_at", index.UnitArtifact, func() []string {
			return createdSince(under(env, "artifacts/*/artifact.zip", a.Rerun.ID), day("2026-09-15"))
		}),
		Entry("extracted: the artifact's created_at", index.UnitExtracted, func() []string {
			return createdSince(extractedFiles(env, a.Rerun.ID), day("2026-09-15"))
		}),
	)

	It("gives the Rerun attempt-2 artifacts a later created_at than its attempt-1 ones", func() {
		zips := under(env, "artifacts/*/artifact.zip", a.Rerun.ID)
		Expect(createdSince(zips, day("2026-09-15"))).NotTo(BeEmpty())
		Expect(len(createdSince(zips, day("2026-09-15")))).To(BeNumerically("<", len(zips)))
	})

	It("compares --until against the unit's time too, inclusive", func() {
		started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		Expect(paths(index.Filter{SHAs: []string{"8"}, Until: started}, index.UnitAttempt)).To(ConsistOf(under(env, "attempt-*/*.json", a.Rerun.ID)))
		Expect(paths(index.Filter{SHAs: []string{"8"}, Until: started.Add(-time.Second)}, index.UnitAttempt)).To(ConsistOf(under(env, "attempt-1/*.json", a.Rerun.ID)))
		Expect(paths(index.Filter{SHAs: []string{"8"}, Since: started}, index.UnitAttempt)).To(ConsistOf(under(env, "attempt-2/*.json", a.Rerun.ID)))
	})

	It("matches --branch only where the head repository is the repository", func() {
		forks := slices.Concat(logsOf(env, a.Fork.ID), under(env, "artifacts/*/artifact.zip", pendingFork))
		Expect(paths(index.Filter{Branches: []string{"main"}}, index.UnitLog)).NotTo(ContainElements(forks))
		Expect(paths(index.Filter{Branches: []string{"main"}}, index.UnitArtifact)).NotTo(ContainElement(HavePrefix(runDir(env.Data(), pendingFork))))
		Expect(paths(index.Filter{Events: []string{"pull_request"}}, index.UnitLog)).To(ContainElements(logsOf(env, a.Fork.ID)))
		Expect(paths(index.Filter{SHAs: []string{"1a51"}}, index.UnitArtifact)).To(ContainElements(under(env, "artifacts/*/artifact.zip", pendingFork)))
	})

	It("orders paths by unit time, run_id, attempt and job_id", func() {
		type key struct {
			at           string
			run, attempt int64
			job          int64
			path         string
		}
		var keys []key
		for _, log := range logsOf(env, sameTimeHigh, sameTimeLow, a.Rerun.ID, a.MainSeptember.ID) {
			jobDir := filepath.Dir(log)
			attemptDir := filepath.Dir(filepath.Dir(jobDir))
			var attempt struct {
				RunStartedAt string `json:"run_started_at"`
			}
			raw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(json.Unmarshal(raw, &attempt)).To(Succeed())
			run, _ := layout.DirID(filepath.Base(filepath.Dir(attemptDir)))
			n, _ := layout.AttemptNumber(filepath.Base(attemptDir))
			job, _ := layout.DirID(filepath.Base(jobDir))
			keys = append(keys, key{attempt.RunStartedAt, run, int64(n), job, log})
		}
		slices.SortFunc(keys, func(x, y key) int {
			return cmp.Or(strings.Compare(x.at, y.at), cmp.Compare(x.run, y.run), cmp.Compare(x.attempt, y.attempt), cmp.Compare(x.job, y.job))
		})
		var want []string
		for _, k := range keys {
			want = append(want, k.path)
		}
		Expect(want[0]).To(HavePrefix(runDir(env.Data(), a.Rerun.ID)))

		got := paths(index.Filter{SHAs: []string{"8", "2", "1a51"}, Branches: []string{"main", "lg-fixture"}}, index.UnitLog)
		Expect(got).To(Equal(want))
	})

	DescribeTable("each unit maps to its file set, and the default is log plus extracted",
		func(u index.Unit, want func() []string) {
			expected := want()
			Expect(expected).NotTo(BeEmpty())
			Expect(paths(index.Filter{}, u)).To(ConsistOf(expected))
		},
		Entry("default", index.UnitDefault, func() []string {
			return slices.Concat(logsOf(env, allRuns(env)...), extractedFiles(env, allRuns(env)...))
		}),
		Entry("run", index.UnitRun, func() []string {
			var files []string
			for _, id := range allRuns(env) {
				Expect(filepath.WalkDir(runDir(env.Data(), id), func(path string, d fs.DirEntry, err error) error {
					if err == nil && d.IsDir() && d.Name() == "extracted" {
						return filepath.SkipDir
					}
					if err == nil && d.Type().IsRegular() && strings.HasSuffix(path, ".json") {
						files = append(files, path)
					}
					return err
				})).To(Succeed())
			}
			return files
		}),
		Entry("attempt", index.UnitAttempt, func() []string {
			return slices.Concat(under(env, "attempt-*/attempt.json", allRuns(env)...), under(env, "attempt-*/jobs.json", allRuns(env)...),
				under(env, "attempt-*/artifacts.json", allRuns(env)...), under(env, "attempt-*/fetch.json", allRuns(env)...))
		}),
		Entry("job", index.UnitJob, func() []string { return under(env, "attempt-*/jobs/*/job.json", allRuns(env)...) }),
		Entry("log", index.UnitLog, func() []string { return logsOf(env, allRuns(env)...) }),
		Entry("artifact", index.UnitArtifact, func() []string { return under(env, "artifacts/*/artifact.zip", allRuns(env)...) }),
		Entry("extracted", index.UnitExtracted, func() []string { return extractedFiles(env, allRuns(env)...) }),
	)
})

// allRuns gives the id of every run under data/.
func allRuns(env *harness.InProcessEnv) []int64 {
	GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(env.Data(), "*", "*", "*", "runs", "*", "*"))
	Expect(err).NotTo(HaveOccurred())
	var ids []int64
	for _, dir := range dirs {
		id, ok := layout.DirID(filepath.Base(dir))
		Expect(ok).To(BeTrue(), dir)
		ids = append(ids, id)
	}
	return ids
}

var _ = Describe("Index.Paths", Label("paths"), func() {
	It("omits a path that no longer exists or is no longer a regular file", func(ctx SpecContext) {
		env, ix := archaeology(ctx)
		logs := logsOf(env, scenario.Archaeology().MainSeptember.ID)
		Expect(os.Remove(logs[0])).To(Succeed())
		Expect(os.Symlink(logs[2], logs[1]+".new")).To(Succeed())
		Expect(os.Rename(logs[1]+".new", logs[1])).To(Succeed())

		got, err := ix.Paths(ctx, index.Filter{SHAs: []string{"2"}}, index.UnitLog)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ConsistOf(logs[2:]))
	}, NodeTimeout(time.Minute))

	It("gives the files it can read, with the error of each unit dir it cannot", func(ctx SpecContext) {
		env, ix := archaeology(ctx)
		release3 := scenario.Archaeology().Release3.ID
		logs := logsOf(env, release3)
		jobDir, artifactDir := filepath.Dir(logs[0]), artifactDirs(env, release3)[0]
		for _, dir := range []string{jobDir, artifactDir} {
			Expect(os.RemoveAll(dir)).To(Succeed())
			Expect(os.WriteFile(dir, nil, 0o644)).To(Succeed())
		}

		got, err := ix.Paths(ctx, index.Filter{SHAs: []string{"3"}}, index.UnitDefault)
		Expect(err).To(MatchError(And(ContainSubstring(logs[0]), ContainSubstring(filepath.Join(artifactDir, "extracted")))))
		Expect(got).To(ConsistOf(logs[1:]))
	}, NodeTimeout(time.Minute))

	It("refuses an unknown unit", func(ctx SpecContext) {
		ix, err := index.Open(ctx, filepath.Join(GinkgoT().TempDir(), "lg.db"), GinkgoT().TempDir(), nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		_, err = ix.Paths(ctx, index.Filter{}, "step")
		Expect(err).To(MatchError("unknown unit step"))
	})
})

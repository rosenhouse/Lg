package mirror_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

const cloneID = 1

var syncTimeout = NodeTimeout(20 * time.Second)

// attemptDir is the published dir of a run's attempt n.
func attemptDir(env *harness.InProcessEnv, id int64, n int) string {
	GinkgoHelper()
	var dir string
	Expect(env.AttemptDirs(id)).To(ContainElement(HaveSuffix("/attempt-"+strconv.Itoa(n)), &dir))
	return dir
}

func glob(pattern string) []string {
	GinkgoHelper()
	matches, err := filepath.Glob(pattern)
	Expect(err).NotTo(HaveOccurred())
	return matches
}

func jobDirOf(id string) string { return "/jobs/" + id + "_" }

var _ = Describe("syncing after-attempt-1, then after-attempt-2", Label("attempts"), func() {
	var (
		env      *harness.InProcessEnv
		attempt1 treesnap.Snap
	)

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		attempt1 = treesnap.Snapshot(attemptDir(env, runID, 1))
		Expect(env.Fake.Advance(runID, "after-attempt-2")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
	}, syncTimeout)

	It("publishes attempt-2 in the same run dir and leaves attempt-1 unchanged", func() {
		Expect(env.AttemptDirs(runID)).To(HaveLen(2))
		Expect(filepath.Dir(attemptDir(env, runID, 2))).To(Equal(filepath.Dir(attemptDir(env, runID, 1))))
		Expect(treesnap.Snapshot(attemptDir(env, runID, 1))).To(Equal(attempt1))
	})

	It("stores job.json in all 12 job dirs, logs only for flaky, timeout and after-flaky, and a not_applicable tombstone for 111221662421", func() {
		attempt2 := attemptDir(env, runID, 2)

		Expect(glob(filepath.Join(attempt2, "jobs", "*", "job.json"))).To(HaveLen(12))
		Expect(glob(filepath.Join(attempt2, "jobs", "*"))).To(HaveLen(12))
		Expect(glob(filepath.Join(attempt2, "jobs", "*", "log.txt"))).To(ConsistOf(
			ContainSubstring(jobDirOf("111221661475")),
			ContainSubstring(jobDirOf("111221661684")),
			ContainSubstring(jobDirOf("111221682214")),
		))
		Expect(glob(filepath.Join(attempt2, "jobs", "*", "*.tombstone"))).To(ConsistOf(tombstonePath(attempt2, "111221662421")))
		Expect(readTombstone(attempt2, "111221662421")).To(HaveKeyWithValue("reason", "not_applicable"))
	})

	It("requests no log for the 8 carried-forward jobs and lists their ids in attempt-2/fetch.json carried_forward_jobs", func() {
		carried := []int64{111221662305, 111221662568, 111221662643, 111221662737, 111221680545, 111221681056, 111221681392, 111221687727}

		for _, id := range carried {
			Expect(env.Fake.Requests()).NotTo(ContainElement(HaveField("Path", HaveSuffix("/jobs/"+strconv.FormatInt(id, 10)+"/logs"))))
		}
		raw, err := os.ReadFile(filepath.Join(attemptDir(env, runID, 2), "fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		var fetch struct {
			CarriedForwardJobs []int64 `json:"carried_forward_jobs"`
		}
		Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
		Expect(fetch.CarriedForwardJobs).To(ConsistOf(carried))
	})

	Describe("then after-attempt-3", func() {
		BeforeEach(func(ctx SpecContext) {
			Expect(env.Fake.Advance(runID, "after-attempt-3")).To(Succeed())
			Expect(env.Sync(ctx)).To(Succeed())
		}, syncTimeout)

		It("publishes attempt-3 with 11 logs and a not_applicable tombstone for 111221892915", func() {
			attempt3 := attemptDir(env, runID, 3)

			Expect(glob(filepath.Join(attempt3, "jobs", "*", "log.txt"))).To(HaveLen(11))
			Expect(glob(filepath.Join(attempt3, "jobs", "*", "*.tombstone"))).To(ConsistOf(tombstonePath(attempt3, "111221892915")))
			Expect(readTombstone(attempt3, "111221892915")).To(HaveKeyWithValue("reason", "not_applicable"))
		})
	})
})

var _ = Describe("a first sync at after-attempt-3", Label("attempts"), func() {
	It("publishes attempts 1, 2 and 3 in one cycle", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-3")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2"), HaveSuffix("/attempt-3")))
	}, cycleTimeout)

	It("asks for no attempt of the run on the next sync", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-3")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		requested := len(env.Fake.Requests())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.Requests()[requested:]).NotTo(ContainElement(HaveField("Path", ContainSubstring("/attempts/"))))
	}, cycleTimeout)

	It("writes carried_forward_jobs as an empty array for an attempt that carried no job forward", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-3")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		raw, err := os.ReadFile(filepath.Join(attemptDir(env, runID, 3), "fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(ContainSubstring(`"carried_forward_jobs": []`))
	}, cycleTimeout)
})

var _ = Describe("an attempt still in progress", Label("attempts"), func() {
	It("is not published while earlier attempts are, and is published by the first sync after it completes", func(ctx SpecContext) {
		env := harness.InProcess()
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), cloneID)
		Expect(env.Fake.AddRun(scenario.InProgress(run, 2))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(cloneID)).To(ConsistOf(HaveSuffix("/attempt-1")))

		Expect(env.Fake.AddRun(run)).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(cloneID)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
	}, cycleTimeout)
})

var _ = Describe("a run whose attempt 2 was created the next UTC day", Label("attempts"), func() {
	It("keeps both attempts under the date dir of the run's created_at", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())

		Expect(env.Fake.AddRun(scenario.NextDayRerun(scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), cloneID), 2))).To(Succeed())
		env.Clock.Set(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(cloneID)).To(ConsistOf(
			ContainSubstring("/runs/2026-10-03/1_lg-fixture_lg-fixture/attempt-1"),
			ContainSubstring("/runs/2026-10-03/1_lg-fixture_lg-fixture/attempt-2"),
		))
	}, cycleTimeout)
})

var _ = Describe("a run whose workflow was renamed before attempt 2", Label("attempts"), func() {
	It("adds attempt-2 to the existing run dir", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())

		Expect(env.Fake.AddRun(scenario.RenameWorkflow(scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), cloneID), 2, "renamed"))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(cloneID)).To(ConsistOf(
			HaveSuffix("/1_lg-fixture_lg-fixture/attempt-1"),
			HaveSuffix("/1_lg-fixture_lg-fixture/attempt-2"),
		))
	}, cycleTimeout)
})

var _ = Describe("an attempt that ended in startup_failure with no jobs", Label("attempts"), func() {
	It("is published with attempt.json, an empty jobs.json array and no job dirs", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.StartupFailure(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID), 1))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		attempt1 := attemptDir(env, cloneID, 1)
		Expect(filepath.Join(attempt1, "attempt.json")).To(BeARegularFile())
		jobs, err := os.ReadFile(filepath.Join(attempt1, "jobs.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(jobs).To(MatchJSON(`[]`))
		Expect(filepath.Join(attempt1, "jobs")).NotTo(BeAnExistingFile())
	}, cycleTimeout)
})

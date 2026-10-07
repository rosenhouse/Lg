package scenario_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const runID = 37129390741

// field reads one top-level field of a JSON file of run.
func field(run scenario.Run, file, name string) any {
	GinkgoHelper()
	var object map[string]any
	Expect(json.Unmarshal(run.Files[file].Data, &object)).To(Succeed(), file)
	return object[name]
}

// jobs reads the jobs of an attempt of run.
func jobs(run scenario.Run, attempt string) []map[string]any {
	GinkgoHelper()
	var listing struct {
		TotalCount int `json:"total_count"`
		Jobs       []map[string]any
	}
	Expect(json.Unmarshal(run.Files[attempt+"/jobs.json"].Data, &listing)).To(Succeed())
	Expect(listing.Jobs).To(HaveLen(listing.TotalCount))
	return listing.Jobs
}

var _ = Describe("Recorded", Label("attempts"), func() {
	It("holds every file of the recording", func() {
		run := scenario.Recorded(runID, "after-attempt-2")

		Expect(run.ID).To(BeEquivalentTo(runID))
		Expect(run.Files).To(HaveKey("status.txt"))
		Expect(run.Files).To(HaveKey("attempt-2/logs/111221661475.txt"))
		Expect(field(run, "run.json", "run_attempt")).To(BeEquivalentTo(2))
	})
})

var _ = Describe("Clone", Label("attempts"), func() {
	It("gives the run, its jobs and its artifacts new ids, the same at every stage", func() {
		for _, stage := range []string{"after-attempt-1", "after-attempt-2"} {
			clone := scenario.Clone(scenario.Recorded(runID, stage), 7)

			Expect(clone.ID).To(BeEquivalentTo(7))
			Expect(field(clone, "run.json", "id")).To(BeEquivalentTo(7))
			Expect(field(clone, "attempt-1/attempt.json", "id")).To(BeEquivalentTo(7))
			Expect(jobs(clone, "attempt-1")[0]).To(HaveKeyWithValue("id", BeEquivalentTo(7_111221289861)))
			Expect(jobs(clone, "attempt-1")[0]).To(HaveKeyWithValue("run_id", BeEquivalentTo(7)))
			Expect(clone.Files).To(HaveKey("attempt-1/logs/7111221289861.txt"))
			Expect(clone.Files).To(HaveKey("artifacts/7011275917910.zip"))
			Expect(string(clone.Files["status.txt"].Data)).To(ContainSubstring("302->200 jobs/7111221289861/logs\n"))
			Expect(string(clone.Files["status.txt"].Data)).To(ContainSubstring("302->200 artifacts/7011275917910/zip\n"))
		}
	})

	It("leaves no recorded id in any JSON file or in status.txt", func() {
		clone := scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 7)

		for name, file := range clone.Files {
			if strings.HasSuffix(name, ".json") || name == "status.txt" {
				Expect(string(file.Data)).NotTo(MatchRegexp(`\b(37129390741|111221661475|11275917910)\b`), name)
			}
		}
	})

	It("gives each id its own new id when one id is a prefix of another", func() {
		run := scenario.Run{ID: 1234, Files: fstest.MapFS{
			"run.json":            {Data: []byte(`{"id":1234}`)},
			"attempt-1/jobs.json": {Data: []byte(`{"total_count":2,"jobs":[{"id":12,"run_id":1234},{"id":123,"run_id":1234}]}`)},
		}}

		clone := scenario.Clone(run, 7)
		Expect(field(clone, "run.json", "id")).To(BeEquivalentTo(7))
		Expect(jobs(clone, "attempt-1")).To(ConsistOf(
			SatisfyAll(HaveKeyWithValue("id", BeEquivalentTo(7_000000000012)), HaveKeyWithValue("run_id", BeEquivalentTo(7))),
			SatisfyAll(HaveKeyWithValue("id", BeEquivalentTo(7_000000000123)), HaveKeyWithValue("run_id", BeEquivalentTo(7))),
		))
	})

	It("refuses a clone id outside (0, 1_000_000), which could overflow a job's new id", func() {
		run := scenario.Recorded(runID, "after-attempt-1")

		Expect(func() { scenario.Clone(run, 0) }).To(Panic())
		Expect(func() { scenario.Clone(run, 1_000_000) }).To(Panic())
		Expect(func() { scenario.Clone(run, 999_999) }).NotTo(Panic())
	})

	It("keeps logs byte-identical", func() {
		recorded := scenario.Recorded(runID, "after-attempt-1")
		clone := scenario.Clone(recorded, 7)

		Expect(clone.Files["attempt-1/logs/7111221289888.txt"].Data).To(Equal(recorded.Files["attempt-1/logs/111221289888.txt"].Data))
	})
})

var _ = Describe("mutations", Label("attempts"), func() {
	var run scenario.Run

	BeforeEach(func() {
		run = scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 7)
	})

	It("leave the run they are given unchanged", func() {
		before := string(run.Files["attempt-2/attempt.json"].Data)

		scenario.NextDayRerun(run, 2)
		scenario.RenameWorkflow(run, 2, "renamed")
		scenario.InProgress(run, 2)
		scenario.StartupFailure(run, 2)
		scenario.Cancel(run, 2)
		scenario.QueueJob(run, 2, "flaky")
		scenario.RenameJob(run, 2, "flaky", "steady")
		scenario.RenumberJob(run, 2, "flaky", 8)
		scenario.WithoutRunAttempt(run)

		Expect(field(run, "run.json", "run_attempt")).To(BeEquivalentTo(2))
		Expect(jobs(run, "attempt-2")).To(ContainElement(SatisfyAll(HaveKeyWithValue("name", "flaky"), HaveKeyWithValue("status", "completed"))))
		Expect(string(run.Files["attempt-2/attempt.json"].Data)).To(Equal(before))
		Expect(run.Files).To(HaveKey("attempt-2/logs/7111221661475.txt"))
	})

	Describe("NextDayRerun", func() {
		It("moves the attempt and the jobs it ran 24 hours later, and nothing earlier", func() {
			rerun := scenario.NextDayRerun(run, 2)

			Expect(field(rerun, "attempt-2/attempt.json", "created_at")).To(Equal("2026-10-04T14:25:09Z"))
			Expect(field(rerun, "attempt-2/attempt.json", "run_started_at")).To(Equal("2026-10-04T14:25:08Z"))
			Expect(field(rerun, "run.json", "run_started_at")).To(Equal("2026-10-04T14:25:08Z"))
			Expect(field(rerun, "run.json", "created_at")).To(Equal("2026-10-03T14:22:54Z"))
			Expect(field(rerun, "attempt-1/attempt.json", "updated_at")).To(Equal("2026-10-03T14:24:12Z"))
			Expect(jobs(rerun, "attempt-2")).To(ContainElements(
				SatisfyAll(HaveKeyWithValue("name", "flaky"), HaveKeyWithValue("started_at", "2026-10-04T14:25:12Z")),
				SatisfyAll(HaveKeyWithValue("name", "pass"), HaveKeyWithValue("started_at", "2026-10-03T14:22:57Z")),
			))
		})
	})

	Describe("RenameWorkflow", func() {
		It("renames the workflow in the run, and in the attempt and its jobs onward", func() {
			renamed := scenario.RenameWorkflow(run, 2, "renamed")

			Expect(field(renamed, "run.json", "name")).To(Equal("renamed"))
			Expect(field(renamed, "attempt-2/attempt.json", "name")).To(Equal("renamed"))
			Expect(field(renamed, "attempt-1/attempt.json", "name")).To(Equal("lg-fixture"))
			Expect(jobs(renamed, "attempt-2")).To(HaveEach(HaveKeyWithValue("workflow_name", "renamed")))
			Expect(jobs(renamed, "attempt-1")).To(HaveEach(HaveKeyWithValue("workflow_name", "lg-fixture")))
		})

		It("renames every attempt after it", func() {
			renamed := scenario.RenameWorkflow(scenario.Clone(scenario.Recorded(runID, "after-attempt-3"), 7), 2, "renamed")

			Expect(field(renamed, "attempt-3/attempt.json", "name")).To(Equal("renamed"))
			Expect(jobs(renamed, "attempt-3")).To(HaveEach(HaveKeyWithValue("workflow_name", "renamed")))
		})

		It("keeps ids above 2^53 exact", func() {
			renamed := scenario.RenameWorkflow(scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 999_999), 2, "renamed")

			Expect(string(renamed.Files["attempt-2/jobs.json"].Data)).To(ContainSubstring(`"id":999999111221661475`))
		})

		It("keeps <, > and & unescaped", func() {
			renamed := scenario.RenameWorkflow(run, 2, "renamed")

			Expect(string(renamed.Files["run.json"].Data)).To(ContainSubstring("<noreply@"))
		})
	})

	Describe("InProgress", func() {
		It("leaves the attempt, and the run when it is the latest, in progress with no conclusion", func() {
			running := scenario.InProgress(run, 2)

			for _, file := range []string{"run.json", "attempt-2/attempt.json"} {
				Expect(field(running, file, "status")).To(Equal("in_progress"), file)
				Expect(field(running, file, "conclusion")).To(BeNil(), file)
			}
			Expect(field(running, "attempt-1/attempt.json", "status")).To(Equal("completed"))
		})

		It("leaves the run completed when the attempt is not its latest", func() {
			running := scenario.InProgress(run, 1)

			Expect(field(running, "attempt-1/attempt.json", "status")).To(Equal("in_progress"))
			Expect(field(running, "run.json", "status")).To(Equal("completed"))
		})
	})

	Describe("Cancel", func() {
		It("concludes the attempt, and the run when it is the latest, cancelled", func() {
			cancelled := scenario.Cancel(run, 2)

			for _, file := range []string{"run.json", "attempt-2/attempt.json"} {
				Expect(field(cancelled, file, "status")).To(Equal("completed"), file)
				Expect(field(cancelled, file, "conclusion")).To(Equal("cancelled"), file)
			}
			Expect(field(scenario.Cancel(run, 1), "run.json", "conclusion")).To(Equal("success"))
		})
	})

	Describe("StartupFailure", func() {
		It("concludes the attempt startup_failure with no jobs and no logs", func() {
			failed := scenario.StartupFailure(run, 2)

			for _, file := range []string{"run.json", "attempt-2/attempt.json"} {
				Expect(field(failed, file, "status")).To(Equal("completed"), file)
				Expect(field(failed, file, "conclusion")).To(Equal("startup_failure"), file)
			}
			Expect(jobs(failed, "attempt-2")).To(BeEmpty())
			Expect(failed.Files).NotTo(HaveKey(HavePrefix("attempt-2/logs/")))
			Expect(jobs(failed, "attempt-1")).To(HaveLen(12))
		})
	})

	Describe("QueueJob", func() {
		It("leaves the attempt's job of that name queued, with no steps, runner, start, end or conclusion", func() {
			queued := scenario.QueueJob(run, 2, "flaky")

			Expect(jobs(queued, "attempt-2")).To(ContainElement(SatisfyAll(
				HaveKeyWithValue("name", "flaky"),
				HaveKeyWithValue("status", "queued"),
				HaveKeyWithValue("conclusion", BeNil()),
				HaveKeyWithValue("steps", BeEmpty()),
				HaveKeyWithValue("runner_name", BeNil()),
				HaveKeyWithValue("started_at", BeNil()),
				HaveKeyWithValue("completed_at", BeNil()),
			)))
			Expect(jobs(queued, "attempt-2")).To(ContainElement(SatisfyAll(HaveKeyWithValue("name", "timeout"), HaveKeyWithValue("status", "completed"))))
			Expect(jobs(queued, "attempt-1")).To(HaveEach(HaveKeyWithValue("status", "completed")))
		})
	})

	Describe("RenameJob", func() {
		It("renames the attempt's jobs of that name only", func() {
			renamed := scenario.RenameJob(run, 2, "flaky", "steady")

			Expect(jobs(renamed, "attempt-2")).To(ContainElement(HaveKeyWithValue("name", "steady")))
			Expect(jobs(renamed, "attempt-2")).NotTo(ContainElement(HaveKeyWithValue("name", "flaky")))
			Expect(jobs(renamed, "attempt-1")).To(ContainElement(HaveKeyWithValue("name", "flaky")))
		})
	})

	Describe("RenumberJob", func() {
		It("gives the attempt's job of that name the id, and its log too", func() {
			renumbered := scenario.RenumberJob(run, 2, "flaky", 8)

			Expect(jobs(renumbered, "attempt-2")).To(ContainElement(SatisfyAll(HaveKeyWithValue("name", "flaky"), HaveKeyWithValue("id", BeEquivalentTo(8)))))
			Expect(renumbered.Files["attempt-2/logs/8.txt"].Data).To(Equal(run.Files["attempt-2/logs/7111221661475.txt"].Data))
			Expect(renumbered.Files).NotTo(HaveKey("attempt-2/logs/7111221661475.txt"))
			Expect(string(renumbered.Files["status.txt"].Data)).To(SatisfyAll(ContainSubstring(" jobs/8/logs\n"), Not(ContainSubstring("/7111221661475/"))))
			Expect(jobs(renumbered, "attempt-1")).To(ContainElement(SatisfyAll(HaveKeyWithValue("name", "flaky"), HaveKeyWithValue("id", BeEquivalentTo(7111221289888)))))
		})
	})

	Describe("WithoutRunAttempt", func() {
		It("drops run_attempt from the listed run only", func() {
			unnumbered := scenario.WithoutRunAttempt(run)

			var listed map[string]any
			Expect(json.Unmarshal(unnumbered.Files["run.json"].Data, &listed)).To(Succeed())
			Expect(listed).NotTo(HaveKey("run_attempt"))
			Expect(listed).To(HaveKeyWithValue("id", BeEquivalentTo(7)))
			Expect(field(unnumbered, "attempt-2/attempt.json", "run_attempt")).To(BeEquivalentTo(2))
		})
	})

	Describe("Expire", Label("artifacts"), func() {
		It("lists the artifact as expired: true, leaving the others and the original run unchanged", func() {
			expired := scenario.Expire(run, 7_011275917910)

			var listing struct {
				TotalCount int `json:"total_count"`
				Artifacts  []map[string]any
			}
			Expect(json.Unmarshal(expired.Files["artifacts.json"].Data, &listing)).To(Succeed())
			Expect(listing.Artifacts).To(HaveLen(listing.TotalCount))
			Expect(listing.Artifacts).To(ContainElement(SatisfyAll(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), HaveKeyWithValue("expired", true))))
			Expect(listing.Artifacts).To(HaveEach(Or(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), HaveKeyWithValue("expired", false))))
			Expect(string(run.Files["artifacts.json"].Data)).NotTo(ContainSubstring(`"expired":true`))
		})
	})

	Describe("ListsArtifact", Label("artifacts"), func() {
		It("reports whether the run's artifacts.json lists the id", func() {
			Expect(run.ListsArtifact(7_011275917910)).To(BeTrue())
			Expect(run.ListsArtifact(11275917910)).To(BeFalse())
		})
	})

	Describe("WithoutDigest", Label("artifacts"), func() {
		It("drops the artifact's digest, leaving the others and the original run unchanged", func() {
			undigested := scenario.WithoutDigest(run, 7_011275917910)

			var listing struct{ Artifacts []map[string]any }
			Expect(json.Unmarshal(undigested.Files["artifacts.json"].Data, &listing)).To(Succeed())
			Expect(listing.Artifacts).To(ContainElement(SatisfyAll(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), Not(HaveKey("digest")))))
			Expect(listing.Artifacts).To(HaveEach(Or(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), HaveKeyWithValue("digest", HavePrefix("sha256:")))))
			Expect(strings.Count(string(run.Files["artifacts.json"].Data), `"digest"`)).To(Equal(len(listing.Artifacts)))
		})
	})

	Describe("WithDigest", Label("artifacts"), func() {
		It("lists the artifact with the digest, leaving the others and the original run unchanged", func() {
			digested := scenario.WithDigest(run, 7_011275917910, "sha512:abcd")

			var listing struct{ Artifacts []map[string]any }
			Expect(json.Unmarshal(digested.Files["artifacts.json"].Data, &listing)).To(Succeed())
			Expect(listing.Artifacts).To(ContainElement(SatisfyAll(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), HaveKeyWithValue("digest", "sha512:abcd"))))
			Expect(listing.Artifacts).To(HaveEach(Or(HaveKeyWithValue("id", BeEquivalentTo(7_011275917910)), HaveKeyWithValue("digest", HavePrefix("sha256:")))))
			Expect(string(run.Files["artifacts.json"].Data)).NotTo(ContainSubstring("sha512"))
		})
	})

	Describe("WithPullRequests", Label("artifacts"), func() {
		It("lists the run with pull requests of the given numbers", func() {
			opened := scenario.WithPullRequests(run, 42, 7)

			Expect(field(opened, "run.json", "pull_requests")).To(ConsistOf(
				HaveKeyWithValue("number", BeEquivalentTo(42)),
				HaveKeyWithValue("number", BeEquivalentTo(7)),
			))
			Expect(field(run, "run.json", "pull_requests")).To(BeEmpty())
		})
	})
})

var _ = Describe("CreatedAt", Label("discovery"), func() {
	It("moves every time in the run's JSON files by the same amount, so that the run was created then", func() {
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 7)
		at := time.Date(2026, 9, 3, 14, 22, 54, 0, time.UTC)

		moved := scenario.CreatedAt(run, at)

		Expect(field(moved, "run.json", "created_at")).To(Equal("2026-09-03T14:22:54Z"))
		Expect(field(moved, "run.json", "run_started_at")).To(Equal("2026-09-03T14:25:08Z"))
		Expect(field(moved, "attempt-1/attempt.json", "updated_at")).To(Equal("2026-09-03T14:24:12Z"))
		Expect(jobs(moved, "attempt-2")).To(ContainElement(SatisfyAll(HaveKeyWithValue("name", "pass"), HaveKeyWithValue("started_at", "2026-09-03T14:22:57Z"))))
		Expect(field(run, "run.json", "created_at")).To(Equal("2026-10-03T14:22:54Z"))
	})
})

var _ = Describe("ListedRun", Label("discovery"), func() {
	It("is a completed run of rosenhouse/Lg with the id and created_at given", func() {
		var run map[string]any
		Expect(json.Unmarshal(scenario.ListedRun(42, time.Date(2026, 9, 3, 14, 22, 54, 0, time.UTC)), &run)).To(Succeed())
		Expect(run).To(SatisfyAll(
			HaveKeyWithValue("id", BeEquivalentTo(42)),
			HaveKeyWithValue("created_at", "2026-09-03T14:22:54Z"),
			HaveKeyWithValue("status", "completed"),
			HaveKeyWithValue("repository", HaveKeyWithValue("full_name", "rosenhouse/Lg")),
		))
	})
})

var _ = Describe("QueuedRun", Label("discovery"), func() {
	It("is a queued run with no conclusion", func() {
		var run map[string]any
		Expect(json.Unmarshal(scenario.QueuedRun(42, time.Date(2026, 9, 3, 14, 22, 54, 0, time.UTC)), &run)).To(Succeed())
		Expect(run).To(SatisfyAll(
			HaveKeyWithValue("id", BeEquivalentTo(42)),
			HaveKeyWithValue("created_at", "2026-09-03T14:22:54Z"),
			HaveKeyWithValue("status", "queued"),
			HaveKeyWithValue("conclusion", BeNil()),
		))
	})
})

var _ = Describe("job edits", Label("flakes"), func() {
	var run scenario.Run

	BeforeEach(func() {
		run = scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), 7)
	})

	// jobOf is the attempt's job of that id.
	jobOf := func(r scenario.Run, attempt string, id int64) map[string]any {
		GinkgoHelper()
		for _, job := range jobs(r, attempt) {
			if job["id"] == float64(id) {
				return job
			}
		}
		Fail(fmt.Sprintf("%s has no job %d", attempt, id))
		return nil
	}

	stepOf := func(job map[string]any, name string) map[string]any {
		GinkgoHelper()
		for _, step := range job["steps"].([]any) {
			if step := step.(map[string]any); step["name"] == name {
				return step
			}
		}
		Fail(fmt.Sprintf("job %v has no step %q", job["id"], name))
		return nil
	}

	Describe("JobIDs", func() {
		It("lists the ids of the attempt's jobs of that name in listing order", func() {
			Expect(run.JobIDs(1, "same name")).To(Equal([]int64{7111221289952, 7111221289997}))
			Expect(run.JobIDs(1, "flaky")).To(Equal([]int64{7111221289888}))
			Expect(run.JobIDs(1, "none")).To(BeEmpty())
		})
	})

	Describe("SetJobConclusion", func() {
		It("concludes only the attempt's job of that id so, leaving the run given unchanged", func() {
			first, second := run.JobIDs(1, "same name")[0], run.JobIDs(1, "same name")[1]
			concluded := scenario.SetJobConclusion(run, 1, first, "failure")

			Expect(jobOf(concluded, "attempt-1", first)).To(HaveKeyWithValue("conclusion", "failure"))
			Expect(jobOf(concluded, "attempt-1", second)).To(HaveKeyWithValue("conclusion", "success"))
			Expect(jobOf(run, "attempt-1", first)).To(HaveKeyWithValue("conclusion", "success"))
		})

		It("panics when the attempt has no job of that id", func() {
			Expect(func() { scenario.SetJobConclusion(run, 1, 1, "failure") }).To(PanicWith(ContainSubstring("job 1")))
		})
	})

	Describe("SetStepConclusion", func() {
		It("concludes only the step of that name in the attempt's job of that id so, leaving the run given unchanged", func() {
			pass := run.JobIDs(1, "pass")[0]
			concluded := scenario.SetStepConclusion(run, 1, pass, "Build nested archives", "failure")

			Expect(stepOf(jobOf(concluded, "attempt-1", pass), "Build nested archives")).To(HaveKeyWithValue("conclusion", "failure"))
			Expect(stepOf(jobOf(concluded, "attempt-1", pass), "Emit log markers")).To(HaveKeyWithValue("conclusion", "success"))
			Expect(jobOf(concluded, "attempt-1", pass)).To(HaveKeyWithValue("conclusion", "success"))
			Expect(stepOf(jobOf(run, "attempt-1", pass), "Build nested archives")).To(HaveKeyWithValue("conclusion", "success"))
		})

		It("panics when the job has no step of that name", func() {
			pass := run.JobIDs(1, "pass")[0]
			Expect(func() { scenario.SetStepConclusion(run, 1, pass, "No such step", "failure") }).To(PanicWith(ContainSubstring(`"No such step"`)))
		})
	})

	stepNames := func(job map[string]any) []any {
		var names []any
		for _, step := range job["steps"].([]any) {
			names = append(names, step.(map[string]any)["name"])
		}
		return names
	}

	Describe("ClearSteps", func() {
		It("leaves the attempt's job of that id no steps, leaving the run given unchanged", func() {
			pass, flaky := run.JobIDs(1, "pass")[0], run.JobIDs(1, "flaky")[0]
			cleared := scenario.ClearSteps(run, 1, pass)

			Expect(jobOf(cleared, "attempt-1", pass)).To(HaveKeyWithValue("steps", BeEmpty()))
			Expect(jobOf(cleared, "attempt-1", flaky)["steps"]).NotTo(BeEmpty())
			Expect(jobOf(run, "attempt-1", pass)["steps"]).NotTo(BeEmpty())
		})
	})

	Describe("ReverseSteps", func() {
		It("lists the steps of the attempt's job of that id in reverse, leaving the run given unchanged", func() {
			pass := run.JobIDs(1, "pass")[0]
			reversed := scenario.ReverseSteps(run, 1, pass)

			names := stepNames(jobOf(run, "attempt-1", pass))
			Expect(len(names)).To(BeNumerically(">", 1))
			backward := slices.Clone(names)
			slices.Reverse(backward)
			Expect(stepNames(jobOf(reversed, "attempt-1", pass))).To(Equal(backward))
			Expect(stepNames(jobOf(run, "attempt-1", pass))).To(Equal(names))
		})
	})
})

var _ = Describe("AddRerunAttempt", Label("flakes"), func() {
	var run scenario.Run

	BeforeEach(func() {
		run = scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), 7)
	})

	attemptOf := func(r scenario.Run, n int) (model.Run, []model.Job) {
		GinkgoHelper()
		var attempt model.Run
		var listing struct{ Jobs []model.Job }
		Expect(json.Unmarshal(r.Files[fmt.Sprintf("attempt-%d/attempt.json", n)].Data, &attempt)).To(Succeed())
		Expect(json.Unmarshal(r.Files[fmt.Sprintf("attempt-%d/jobs.json", n)].Data, &listing)).To(Succeed())
		return attempt, listing.Jobs
	}

	kinds := func(r scenario.Run, n int) map[string][]model.JobKind {
		GinkgoHelper()
		attempt, jobs := attemptOf(r, n)
		byName := map[string][]model.JobKind{}
		for _, job := range jobs {
			byName[job.Name] = append(byName[job.Name], model.Classify(job, attempt.RunStartedAt))
		}
		return byName
	}

	It("adds a completed attempt, after the latest, that re-runs the jobs named and carries the others forward under new ids", func() {
		rerun := scenario.AddRerunAttempt(run, "flaky")

		first, _ := attemptOf(rerun, 1)
		second, jobs := attemptOf(rerun, 2)
		Expect(second.RunAttempt).To(Equal(2))
		Expect(second.Status).To(Equal("completed"))
		Expect(second.RunStartedAt).To(BeTemporally(">", first.UpdatedAt))
		Expect(second.UpdatedAt).To(BeTemporally(">", second.RunStartedAt))
		Expect(field(rerun, "run.json", "run_attempt")).To(BeEquivalentTo(2))
		Expect(field(rerun, "run.json", "run_started_at")).To(Equal(second.RunStartedAt.Format(time.RFC3339)))

		Expect(kinds(rerun, 2)).To(SatisfyAll(
			HaveKeyWithValue("flaky", []model.JobKind{model.Ran}),
			HaveKeyWithValue("pass", []model.JobKind{model.CarriedForward}),
			HaveKeyWithValue("same name", []model.JobKind{model.CarriedForward, model.CarriedForward}),
		))
		_, firstJobs := attemptOf(rerun, 1)
		var ids []int64
		for i, job := range jobs {
			Expect(job.Name).To(Equal(firstJobs[i].Name))
			Expect(job.ID).NotTo(BeElementOf(run.JobIDs(1, job.Name)))
			ids = append(ids, job.ID)
		}
		Expect(slices.Compact(slices.Sorted(slices.Values(ids)))).To(HaveLen(len(ids)))
		Expect(run.Files).NotTo(HaveKey("attempt-2/jobs.json"))
	})

	It("gives the new attempt, and each of its jobs, its own attempt number and URLs", func() {
		rerun := scenario.AddRerunAttempt(run, "flaky")

		attempts := "https://api.github.com/repos/rosenhouse/Lg/actions/runs/7/attempts/"
		for _, file := range []string{"attempt-2/attempt.json", "run.json"} {
			Expect(field(rerun, file, "previous_attempt_url")).To(Equal(attempts+"1"), file)
		}
		Expect(field(rerun, "attempt-2/attempt.json", "jobs_url")).To(Equal(attempts + "2/jobs"))
		Expect(field(rerun, "attempt-2/attempt.json", "logs_url")).To(Equal(attempts + "2/logs"))
		for _, job := range jobs(rerun, "attempt-2") {
			id := fmt.Sprint(int64(job["id"].(float64)))
			Expect(job).To(SatisfyAll(
				HaveKeyWithValue("run_attempt", BeEquivalentTo(2)),
				HaveKeyWithValue("url", HaveSuffix("/jobs/"+id)),
				HaveKeyWithValue("html_url", HaveSuffix("/runs/7/job/"+id)),
				HaveKeyWithValue("check_run_url", HaveSuffix("/check-runs/"+id)),
			))
		}
	})

	It("concludes the re-run jobs and their steps success, keeping the carried ones' conclusions", func() {
		rerun := scenario.AddRerunAttempt(scenario.SetJobConclusion(run, 1, run.JobIDs(1, "pass")[0], "failure"), "flaky")

		_, jobs := attemptOf(rerun, 2)
		for _, job := range jobs {
			switch job.Name {
			case "flaky":
				Expect(job.Conclusion).To(Equal("success"))
				Expect(job.Steps).To(HaveEach(HaveField("Conclusion", "success")))
			case "pass":
				Expect(job.Conclusion).To(Equal("failure"))
			}
		}
		Expect(field(rerun, "attempt-2/attempt.json", "conclusion")).To(Equal("failure"))
		Expect(field(scenario.AddRerunAttempt(run, "flaky", "timeout"), "attempt-2/attempt.json", "conclusion")).To(Equal("success"))
	})

	It("serves each new job's log as the job of the latest attempt it copies", func() {
		rerun := scenario.AddRerunAttempt(run, "flaky")

		_, before := attemptOf(rerun, 1)
		_, after := attemptOf(rerun, 2)
		status := string(rerun.Files["status.txt"].Data)
		for i, job := range after {
			old := before[i].ID
			Expect(rerun.Files[fmt.Sprintf("attempt-2/logs/%d.txt", job.ID)].Data).To(Equal(run.Files[fmt.Sprintf("attempt-1/logs/%d.txt", old)].Data))
			oldLine := regexp.MustCompile(fmt.Sprintf(`(?m)^(\S+) jobs/%d/logs$`, old)).FindStringSubmatch(status)
			Expect(status).To(ContainSubstring(fmt.Sprintf("\n%s jobs/%d/logs\n", oldLine[1], job.ID)))
		}
	})

	It("re-runs, in attempt 3, a job attempt 2 carried forward", func() {
		twice := scenario.AddRerunAttempt(scenario.AddRerunAttempt(run, "flaky"), "pass")

		Expect(field(twice, "run.json", "run_attempt")).To(BeEquivalentTo(3))
		Expect(kinds(twice, 3)).To(SatisfyAll(
			HaveKeyWithValue("flaky", []model.JobKind{model.CarriedForward}),
			HaveKeyWithValue("pass", []model.JobKind{model.Ran}),
		))
		_, jobs := attemptOf(twice, 3)
		Expect(twice.Files).To(HaveKey(fmt.Sprintf("attempt-3/logs/%d.txt", jobs[0].ID)))
	})

	It("panics when the latest attempt has no job of a name given", func() {
		Expect(func() { scenario.AddRerunAttempt(run, "none") }).To(PanicWith(ContainSubstring(`"none"`)))
	})
})

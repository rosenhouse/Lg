package scenario_test

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

var _ = Describe("Recorded", func() {
	It("holds every file of the recording", func() {
		run := scenario.Recorded(runID, "after-attempt-2")

		Expect(run.ID).To(BeEquivalentTo(runID))
		Expect(run.Files).To(HaveKey("status.txt"))
		Expect(run.Files).To(HaveKey("attempt-2/logs/111221661475.txt"))
		Expect(field(run, "run.json", "run_attempt")).To(BeEquivalentTo(2))
	})
})

var _ = Describe("Clone", func() {
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

	It("keeps logs byte-identical", func() {
		recorded := scenario.Recorded(runID, "after-attempt-1")
		clone := scenario.Clone(recorded, 7)

		Expect(clone.Files["attempt-1/logs/7111221289888.txt"].Data).To(Equal(recorded.Files["attempt-1/logs/111221289888.txt"].Data))
	})
})

var _ = Describe("mutations", func() {
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
})

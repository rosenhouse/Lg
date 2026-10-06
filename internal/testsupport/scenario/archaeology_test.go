package scenario_test

import (
	"bytes"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// artifacts reads the artifacts listed in run.json's artifacts.json.
func artifacts(run scenario.Run) []map[string]any {
	GinkgoHelper()
	var listing struct{ Artifacts []map[string]any }
	Expect(json.Unmarshal(run.Files["artifacts.json"].Data, &listing)).To(Succeed())
	Expect(listing.Artifacts).NotTo(BeEmpty())
	return listing.Artifacts
}

var _ = Describe("mutations of the run's head", Label("paths"), func() {
	var run scenario.Run

	BeforeEach(func() {
		run = scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 7)
	})

	runFiles := []string{"run.json", "attempt-1/attempt.json", "attempt-2/attempt.json"}

	It("leave the run they are given unchanged", func() {
		before := string(run.Files["attempt-2/attempt.json"].Data)

		scenario.OnBranch(run, "main")
		scenario.WithSHA(run, "abc")
		scenario.WithEvent(run, "pull_request")
		scenario.WithDisplayTitle(run, "title")
		scenario.FromFork(run, "someone/Lg")
		scenario.WithPullRequests(run, 42)
		scenario.RerunAt(run, 2, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		scenario.InjectLogLine(run, 2, "flaky", "foo bar")

		Expect(string(run.Files["attempt-2/attempt.json"].Data)).To(Equal(before))
		Expect(artifacts(run)).To(HaveEach(HaveKeyWithValue("workflow_run", HaveKeyWithValue("head_branch", "lg-fixture"))))
		Expect(bytes.Contains(run.Files["attempt-2/logs/7111221661475.txt"].Data, []byte("foo bar"))).To(BeFalse())
	})

	Describe("OnBranch", func() {
		It("sets head_branch in the listed run, every attempt, their jobs and the artifacts' workflow_run", func() {
			moved := scenario.OnBranch(run, "release-3")

			for _, file := range runFiles {
				Expect(field(moved, file, "head_branch")).To(Equal("release-3"), file)
			}
			Expect(jobs(moved, "attempt-1")).To(HaveEach(HaveKeyWithValue("head_branch", "release-3")))
			Expect(jobs(moved, "attempt-2")).To(HaveEach(HaveKeyWithValue("head_branch", "release-3")))
			Expect(artifacts(moved)).To(HaveEach(HaveKeyWithValue("workflow_run", HaveKeyWithValue("head_branch", "release-3"))))
		})
	})

	Describe("WithSHA", func() {
		It("sets head_sha in the listed run, every attempt, their jobs and the artifacts' workflow_run", func() {
			moved := scenario.WithSHA(run, "0808")

			for _, file := range runFiles {
				Expect(field(moved, file, "head_sha")).To(Equal("0808"), file)
			}
			Expect(jobs(moved, "attempt-2")).To(HaveEach(HaveKeyWithValue("head_sha", "0808")))
			Expect(artifacts(moved)).To(HaveEach(HaveKeyWithValue("workflow_run", HaveKeyWithValue("head_sha", "0808"))))
		})
	})

	Describe("WithEvent and WithDisplayTitle", func() {
		It("set event and display_title in the listed run and every attempt", func() {
			moved := scenario.WithDisplayTitle(scenario.WithEvent(run, "pull_request"), "fix foo bar")

			for _, file := range runFiles {
				Expect(field(moved, file, "event")).To(Equal("pull_request"), file)
				Expect(field(moved, file, "display_title")).To(Equal("fix foo bar"), file)
			}
		})
	})

	Describe("FromFork", func() {
		It("gives the listed run, every attempt and the artifacts' workflow_run a head repository other than the repository", func() {
			forked := scenario.FromFork(run, "someone/Lg")

			for _, file := range runFiles {
				head := field(forked, file, "head_repository").(map[string]any)
				repo := field(forked, file, "repository").(map[string]any)
				Expect(head).To(HaveKeyWithValue("full_name", "someone/Lg"), file)
				Expect(head["id"]).NotTo(Equal(repo["id"]), file)
			}
			for _, a := range artifacts(forked) {
				workflowRun := a["workflow_run"].(map[string]any)
				Expect(workflowRun["head_repository_id"]).NotTo(Equal(workflowRun["repository_id"]))
			}
		})
	})

	Describe("WithPullRequests", func() {
		It("also lists the pull requests in every attempt", func() {
			opened := scenario.WithPullRequests(run, 42)

			for _, file := range runFiles {
				Expect(field(opened, file, "pull_requests")).To(ConsistOf(HaveKeyWithValue("number", BeEquivalentTo(42))), file)
			}
		})
	})

	Describe("RerunAt", func() {
		It("moves the attempt and the jobs it ran so that the attempt starts at the given time, and nothing earlier", func() {
			rerun := scenario.RerunAt(run, 2, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

			Expect(field(rerun, "attempt-2/attempt.json", "run_started_at")).To(Equal("2026-10-01T12:00:00Z"))
			Expect(field(rerun, "attempt-2/attempt.json", "created_at")).To(Equal("2026-10-01T12:00:01Z"))
			Expect(field(rerun, "run.json", "created_at")).To(Equal("2026-10-03T14:22:54Z"))
			Expect(jobs(rerun, "attempt-2")).To(ContainElements(
				SatisfyAll(HaveKeyWithValue("name", "flaky"), HaveKeyWithValue("started_at", "2026-10-01T12:00:04Z")),
				SatisfyAll(HaveKeyWithValue("name", "pass"), HaveKeyWithValue("started_at", "2026-10-03T14:22:57Z")),
			))
		})
	})

	Describe("InjectLogLine", func() {
		It("puts the text after the BOM and the first line's timestamp, as a line of its own, in the log of each job of that name in the attempt", func() {
			log := "attempt-2/logs/7111221661475.txt"
			original := run.Files[log].Data
			injected := scenario.InjectLogLine(run, 2, "flaky", "foo bar")

			bom := "\uFEFF"
			Expect(string(original)).To(HavePrefix(bom + "2026-10-03T14:25:12.")) // the first line's timestamp
			stamp := string(original[len(bom):bytes.IndexByte(original, ' ')])
			Expect(string(injected.Files[log].Data)).To(Equal(bom + stamp + " foo bar\n" + string(original[len(bom):])))
			Expect(injected.Files["attempt-1/logs/7111221289888.txt"].Data).To(Equal(run.Files["attempt-1/logs/7111221289888.txt"].Data))
		})

		It("panics when the attempt has no log of a job of that name", func() {
			Expect(func() { scenario.InjectLogLine(run, 2, "no such job", "foo bar") }).To(Panic())
		})
	})
})

var _ = Describe("Archaeology", Label("paths"), func() {
	It("logs FooBar in the main, release-3, feature and fork runs only, and holds it in TitleOnly's display_title", func() {
		a := scenario.Archaeology()
		logged := func(r scenario.Run) bool {
			for name, file := range r.Files {
				if bytes.HasSuffix([]byte(name), []byte(".txt")) && bytes.Contains(file.Data, []byte(scenario.FooBar)) {
					return true
				}
			}
			return false
		}

		for _, r := range []scenario.Run{a.MainAugust, a.MainSeptember, a.Release3, a.Feature, a.Fork} {
			Expect(logged(r)).To(BeTrue(), "run %d", r.ID)
		}
		for _, r := range []scenario.Run{a.TitleOnly, a.PR42, a.Rerun} {
			Expect(logged(r)).To(BeFalse(), "run %d", r.ID)
		}
		Expect(field(a.TitleOnly, "attempt-1/attempt.json", "display_title")).To(ContainSubstring(scenario.FooBar))
		Expect(a.All()).To(HaveLen(8))
	})

	It("gives every run its own SHA", func() {
		shas := map[any]bool{}
		for _, r := range scenario.Archaeology().All() {
			shas[field(r, "run.json", "head_sha")] = true
		}
		Expect(shas).To(HaveLen(8))
	})
})

package scenario_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// conclusionOf gives the conclusion of the job of that name in the attempt,
// or of the step of it when step is not "", as a letter: S success, F
// failure, C cancelled, - skipped.
func conclusionOf(r scenario.Run, attempt, job, step string) string {
	GinkgoHelper()
	letters := map[any]string{"success": "S", "failure": "F", "cancelled": "C", "skipped": "-"}
	for _, j := range jobs(r, attempt) {
		if j["name"] != job {
			continue
		}
		if step == "" {
			return letters[j["conclusion"]]
		}
		for _, s := range j["steps"].([]any) {
			if s := s.(map[string]any); s["name"] == step {
				return letters[s["conclusion"]]
			}
		}
	}
	Fail("no job " + job + " step " + step)
	return ""
}

var _ = Describe("RenameStep", Label("flakes"), func() {
	It("renames the step of that name in the attempt's job of that id", func() {
		r := scenario.Recorded(runID, "after-attempt-1")
		pass := r.JobIDs(1, "pass")[0]

		renamed := scenario.RenameStep(r, 1, pass, "Emit log markers", "unit")

		Expect(conclusionOf(renamed, "attempt-1", "pass", "unit")).To(Equal("S"))
		Expect(conclusionOf(r, "attempt-1", "pass", "Emit log markers")).To(Equal("S"))
		Expect(func() { scenario.RenameStep(r, 1, pass, "no such step", "unit") }).To(Panic())
	})
})

var _ = Describe("Intermittent", Label("flakes"), func() {
	var i scenario.IntermittentRuns

	BeforeEach(func() {
		i = scenario.Intermittent()
	})

	letters := func(attempt, job, step string) string {
		GinkgoHelper()
		var out string
		for _, r := range i.Main {
			out += conclusionOf(r, attempt, job, step)
		}
		return out
	}

	It("gives six push runs on main from the repository, oldest first, each with its own SHA", func() {
		Expect(i.Main).To(HaveLen(6))
		last := ""
		for n, r := range i.Main {
			Expect(r.ID).To(BeEquivalentTo(n + 1))
			Expect(field(r, "run.json", "event")).To(Equal("push"))
			Expect(field(r, "run.json", "head_branch")).To(Equal("main"))
			Expect(field(r, "run.json", "head_sha")).To(Equal(strings.Repeat(fmt.Sprint(n+1), 40)))
			Expect(field(r, "run.json", "head_repository")).To(HaveKeyWithValue("full_name", "rosenhouse/Lg"))
			created := field(r, "run.json", "created_at").(string)
			Expect(created > last).To(BeTrue(), "%s after %s", created, last)
			last = created
		}
	})

	It("concludes the first attempts' jobs integration SSFSSS, broken SSSFFF, steady SSSSSS and suite SFFFFF, with suite's steps unit SSSFSS and e2e SFF-FF", func() {
		Expect(letters("attempt-1", "integration", "")).To(Equal("SSFSSS"))
		Expect(letters("attempt-1", "broken", "")).To(Equal("SSSFFF"))
		Expect(letters("attempt-1", "steady", "")).To(Equal("SSSSSS"))
		Expect(letters("attempt-1", "suite", "")).To(Equal("SFFFFF"))
		Expect(letters("attempt-1", "suite", "unit")).To(Equal("SSSFSS"))
		Expect(letters("attempt-1", "suite", "e2e")).To(Equal("SFF-FF"))
	})

	It("re-runs integration and suite on commit 3, where they succeed", func() {
		Expect(field(i.Main[2], "run.json", "run_attempt")).To(BeEquivalentTo(2))
		Expect(conclusionOf(i.Main[2], "attempt-2", "integration", "")).To(Equal("S"))
		Expect(conclusionOf(i.Main[2], "attempt-2", "suite", "")).To(Equal("S"))
	})

	It("adds a fork pull_request run on head main that fails steady, and a cancelled main run", func() {
		Expect(field(i.Fork, "run.json", "event")).To(Equal("pull_request"))
		Expect(field(i.Fork, "run.json", "head_branch")).To(Equal("main"))
		Expect(field(i.Fork, "run.json", "head_repository")).NotTo(HaveKeyWithValue("full_name", "rosenhouse/Lg"))
		Expect(conclusionOf(i.Fork, "attempt-1", "steady", "")).To(Equal("F"))

		Expect(field(i.Cancelled, "attempt-1/attempt.json", "conclusion")).To(Equal("cancelled"))
		Expect(field(i.Cancelled, "run.json", "head_branch")).To(Equal("main"))
		Expect(conclusionOf(i.Cancelled, "attempt-1", "steady", "")).To(Equal("C"))
		Expect(i.All()).To(HaveLen(8))
	})
})

package recordings_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("Attempt", Label("attempts"), func() {
	It("reads a stage's attempt-N/attempt.json", func() {
		run, err := recordings.Attempt(37129390741, "after-attempt-3", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(run.ID).To(BeEquivalentTo(37129390741))
		Expect(run.RunAttempt).To(Equal(2))
		Expect(run.RunStartedAt).To(Equal(time.Date(2026, 10, 3, 14, 25, 8, 0, time.UTC)))
	})

	It("fails for an attempt the stage does not hold", func() {
		_, err := recordings.Attempt(37129390741, "after-attempt-1", 2)
		Expect(err).To(MatchError(ContainSubstring("attempt-2/attempt.json")))
	})
})

var _ = Describe("Jobs", Label("attempts"), func() {
	It("reads a stage's attempt-N/jobs.json", func() {
		jobs, err := recordings.Jobs(37129390741, "after-attempt-3", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(jobs).To(HaveLen(12))
		Expect(jobs[0].ID).To(BeEquivalentTo(111221289861))
		Expect(jobs[0].Steps).NotTo(BeEmpty())
	})

	It("fails for an attempt the stage does not hold", func() {
		_, err := recordings.Jobs(37129390741, "after-attempt-1", 2)
		Expect(err).To(MatchError(ContainSubstring("attempt-2/jobs.json")))
	})
})

package model_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("MatchOriginal", Label("index"), func() {
	const run = 37129390741
	var attempts []model.AttemptJobs

	BeforeEach(func() {
		attempts = nil
		for n := 1; n <= 3; n++ {
			attempt, err := recordings.Attempt(run, "after-attempt-3", n)
			Expect(err).NotTo(HaveOccurred())
			jobs, err := recordings.Jobs(run, "after-attempt-3", n)
			Expect(err).NotTo(HaveOccurred())
			attempts = append(attempts, model.AttemptJobs{RunStartedAt: attempt.RunStartedAt, Jobs: jobs})
		}
	})

	job := func(attempt int, id int64) model.Job {
		GinkgoHelper()
		for _, j := range attempts[attempt-1].Jobs {
			if j.ID == id {
				return j
			}
		}
		Fail("no such job")
		return model.Job{}
	}
	originalID := func(j model.Job, earlier []model.AttemptJobs) int64 {
		GinkgoHelper()
		original, ok := model.MatchOriginal(j, earlier)
		Expect(ok).To(BeTrue())
		return original.ID
	}

	DescribeTable("tells the two 'same name' jobs apart by started_at, completed_at and runner_name",
		func(carried, want int64) {
			Expect(originalID(job(2, carried), attempts[:1])).To(Equal(want))
		},
		Entry("111221662643", int64(111221662643), int64(111221289952)),
		Entry("111221681392", int64(111221681392), int64(111221289997)),
	)

	It("follows a chain through two attempts to the job that ran", func() {
		again := job(2, 111221662643)
		again.ID = 3

		Expect(originalID(again, attempts[:2])).To(Equal(int64(111221289952)))
	})

	DescribeTable("returns false when nothing matches",
		func(edit func(*model.Job)) {
			j := job(2, 111221662643)
			edit(&j)
			_, ok := model.MatchOriginal(j, attempts[:1])
			Expect(ok).To(BeFalse())
		},
		Entry("another name", func(j *model.Job) { j.Name = "other" }),
		Entry("another started_at", func(j *model.Job) { t := j.StartedAt.Add(time.Second); j.StartedAt = &t }),
		Entry("another completed_at", func(j *model.Job) { t := j.CompletedAt.Add(time.Second); j.CompletedAt = &t }),
		Entry("no completed_at", func(j *model.Job) { j.CompletedAt = nil }),
		Entry("another runner_name", func(j *model.Job) { r := "other"; j.RunnerName = &r }),
		Entry("no runner_name", func(j *model.Job) { j.RunnerName = nil }),
	)

	It("returns false when only a carried-forward copy matches", func() {
		again := job(2, 111221662643)
		again.ID = 3

		_, ok := model.MatchOriginal(again, attempts[1:2])
		Expect(ok).To(BeFalse())
	})
})

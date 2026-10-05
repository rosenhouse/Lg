package model_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
)

var _ = Describe("Attribute", Label("index"), func() {
	t0 := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	// snapshot is attempt n started at minute 10n, listing ids while run_attempt was listedDuring.
	snapshot := func(n, listedDuring int, ids ...int64) model.Snapshot {
		return model.Snapshot{Attempt: n, RunStartedAt: at(10 * n), ListedDuring: listedDuring, Listed: ids}
	}
	type result struct {
		Attempt int
		By      model.Attribution
	}
	attribute := func(id int64, created time.Time, fetchedDuring int, snapshots ...model.Snapshot) result {
		n, by := model.Attribute(id, created, fetchedDuring, snapshots)
		return result{n, by}
	}

	It("gives attempt N by listing-diff when attempt-N lists the id and attempt-(N-1) does not, both listed during their own attempt", func() {
		Expect(attribute(7, at(99), 2, snapshot(1, 1, 5), snapshot(2, 2, 5, 7))).To(Equal(result{2, model.ByListing}))
		Expect(attribute(5, at(99), 2, snapshot(1, 1, 5), snapshot(2, 2, 5, 7))).To(Equal(result{1, model.ByListing}))
	})

	DescribeTable("falls back to the window [run_started_at(N), run_started_at(N+1)) when a snapshot was listed during another attempt",
		func(first, second model.Snapshot) {
			Expect(attribute(7, at(15), 2, first, second)).To(Equal(result{1, model.ByTimestamp}))
		},
		Entry("attempt-N", snapshot(1, 1), snapshot(2, 3, 7)),
		Entry("attempt-(N-1)", snapshot(1, 2), snapshot(2, 2, 7)),
	)

	It("falls back to the window when attempt-(N-1) is not on disk", func() {
		Expect(attribute(7, at(25), 2, snapshot(2, 2, 7))).To(Equal(result{2, model.ByTimestamp}))
	})

	DescribeTable("gives the window's attempt, including its start and excluding the next attempt's start",
		func(created time.Time, attempt int) {
			Expect(attribute(7, created, 3, snapshot(1, 3), snapshot(2, 3), snapshot(3, 3))).To(Equal(result{attempt, model.ByTimestamp}))
		},
		Entry("at attempt 1's start", at(10), 1),
		Entry("just before attempt 2's start", at(20).Add(-time.Second), 1),
		Entry("at attempt 2's start", at(20), 2),
		Entry("after the latest attempt's start", at(99), 3),
	)

	DescribeTable("gives unknown when no window holds created_at",
		func(created time.Time, fetchedDuring int, snapshots ...model.Snapshot) {
			Expect(attribute(7, created, fetchedDuring, snapshots...)).To(Equal(result{0, model.Unknown}))
		},
		Entry("no attempt on disk", at(15), 1),
		Entry("before the earliest attempt on disk", at(15), 2, snapshot(2, 2)),
		Entry("after attempt N, when attempt N+1 is not on disk", at(15), 3, snapshot(1, 3), snapshot(3, 3)),
		Entry("after the latest attempt on disk, when a later one was listed", at(25), 3, snapshot(1, 3), snapshot(2, 3)),
	)
})

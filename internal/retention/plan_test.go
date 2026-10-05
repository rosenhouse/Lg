package retention_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/retention"
)

func run(date string, id int64, bytes int64, extracted ...retention.Tree) retention.Run {
	return retention.Run{Dir: fmt.Sprintf("%s/%d", date, id), Date: date, ID: id, Bytes: bytes, Extracted: extracted}
}

func dirs(runs []retention.Run) []string {
	out := []string{}
	for _, r := range runs {
		out = append(out, r.Dir)
	}
	return out
}

var _ = Describe("retention.Cutoff", Label("retention"), func() {
	It("is the UTC date retention before now", func() {
		Expect(retention.Cutoff(time.Date(2027, 1, 1, 18, 0, 0, 0, time.UTC), 90*24*time.Hour)).To(Equal("2026-10-03"))
	})

	It("takes the date in UTC, not in now's zone", func() {
		east := time.FixedZone("UTC+5", 5*60*60)
		Expect(retention.Cutoff(time.Date(2027, 1, 1, 1, 0, 0, 0, east), 90*24*time.Hour)).To(Equal("2026-10-02"))
	})
})

var _ = Describe("retention.Plan", Label("retention"), func() {
	const cutoff = "2026-10-03"

	It("expires the runs of date dirs before the cutoff, by date dir and then run id, and keeps the cutoff's", func() {
		runs := []retention.Run{run("2026-10-03", 1, 1), run("2026-10-01", 10, 1), run("2026-10-01", 9, 1), run("2026-09-30", 11, 1)}

		v := retention.Plan(retention.Usage{Runs: runs, Bytes: 4}, cutoff, 100)
		Expect(v.Expired).To(Equal([]string{runs[3].Dir, runs[2].Dir, runs[1].Dir}))
		Expect(v.Extracted).To(BeEmpty())
		Expect(v.Evicted).To(BeEmpty())
	})

	It("removes nothing under disk_cap, nor at it", func() {
		runs := []retention.Run{run("2026-10-03", 1, 10, retention.Tree{Dir: "x", Bytes: 5})}

		Expect(retention.Plan(retention.Usage{Runs: runs, Bytes: 10}, cutoff, 10)).To(BeZero())
	})

	It("over disk_cap removes extracted trees oldest run first, and only as many as it must", func() {
		runs := []retention.Run{
			run("2026-10-05", 1, 10, retention.Tree{Dir: "new", Bytes: 5}),
			run("2026-10-04", 2, 10, retention.Tree{Dir: "old-1", Bytes: 2}, retention.Tree{Dir: "old-2", Bytes: 2}),
		}

		v := retention.Plan(retention.Usage{Runs: runs, Bytes: 20}, cutoff, 16)
		Expect(v.Extracted).To(Equal([]string{"old-1", "old-2"}))
		Expect(v.Evicted).To(BeEmpty())
	})

	It("then evicts runs by date dir and then run id until data/ is at disk_cap, counting a run without its removed extracted trees", func() {
		runs := []retention.Run{
			run("2026-10-05", 1, 10),
			run("2026-10-04", 10, 10),
			run("2026-10-04", 9, 10),
			run("2026-10-03", 11, 10, retention.Tree{Dir: "x", Bytes: 4}),
		}

		v := retention.Plan(retention.Usage{Runs: runs, Bytes: 45}, cutoff, 33)
		Expect(v.Extracted).To(Equal([]string{"x"}))
		Expect(dirs(v.Evicted)).To(Equal([]string{runs[3].Dir, runs[2].Dir}))
	})

	It("also evicts each kept run created at or before an evicted run, so the horizon passes no kept run", func() {
		at := func(hour int) time.Time { return time.Date(2026, 10, 4, hour, 0, 0, 0, time.UTC) }
		runs := []retention.Run{run("2026-10-04", 9, 10), run("2026-10-04", 10, 10), run("2026-10-04", 11, 10), run("2026-10-04", 12, 10), run("2026-10-04", 13, 10)}
		runs[0].CreatedAt, runs[1].CreatedAt, runs[2].CreatedAt, runs[3].CreatedAt = at(12), at(6), at(12), at(13)

		v := retention.Plan(retention.Usage{Runs: runs, Bytes: 50}, cutoff, 40)
		Expect(dirs(v.Evicted)).To(Equal([]string{runs[0].Dir, runs[1].Dir, runs[2].Dir}))
	})

	It("counts expired runs as removed before checking disk_cap", func() {
		runs := []retention.Run{run("2026-10-01", 1, 10), run("2026-10-04", 2, 10)}

		v := retention.Plan(retention.Usage{Runs: runs, Bytes: 20}, cutoff, 10)
		Expect(v.Expired).To(Equal([]string{runs[0].Dir}))
		Expect(v.Evicted).To(BeEmpty())
	})

	It("lists every dir it removes, in order", func() {
		v := retention.Victims{Expired: []string{"a"}, Extracted: []string{"b"}, Evicted: []retention.Run{{Dir: "c"}}}
		Expect(v.Dirs()).To(Equal([]string{"a", "b", "c"}))
	})
})

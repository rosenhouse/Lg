package retention_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/store"
)

func newStore() *store.Store {
	GinkgoHelper()
	root := filepath.Join(GinkgoT().TempDir(), "lg")
	Expect(store.Init(root)).To(Succeed())
	s, err := store.Open(root)
	Expect(err).NotTo(HaveOccurred())
	return s
}

var _ = Describe("retention.Horizons", Label("retention"), func() {
	const repo = "github.com/o/r"
	at := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)

	It("skips nothing when state/horizon.json is missing", func() {
		h, discarded, err := retention.ReadHorizons(newStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
		Expect(h.Skips(repo, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))).To(BeFalse())
	})

	It("are written to state/horizon.json and read back, and skip the runs of a repo dir created at or before its horizon", func() {
		s := newStore()
		Expect(retention.Horizons{repo: at}.Write(s)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(s.State(), "horizon.json"))).To(MatchJSON(`{"github.com/o/r":"2026-09-30T18:00:00Z"}`))

		h, discarded, err := retention.ReadHorizons(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
		Expect(h).To(Equal(retention.Horizons{repo: at}))
		Expect(h.Skips(repo, at)).To(BeTrue())
		Expect(h.Skips(repo, at.Add(-time.Second))).To(BeTrue())
		Expect(h.Skips(repo, at.Add(time.Second))).To(BeFalse())
	})

	It("skip no run of another repo dir", func() {
		h := retention.Horizons{repo: at}
		Expect(h.Skips("github.com/o/other", at.Add(-time.Second))).To(BeFalse())
		Expect(h.Skips("ghe.example.com/o/r", at.Add(-time.Second))).To(BeFalse())
	})

	It("key a repo dir by its lowercased host and its full name as GitHub spells it", func() {
		Expect(retention.RepoKey("GitHub.com", "rosenhouse/Lg")).To(Equal("github.com/rosenhouse/Lg"))
	})

	DescribeTable("moves state/horizon.json aside when it does not parse, and skips nothing",
		func(content string) {
			s := newStore()
			path := filepath.Join(s.State(), "horizon.json")
			Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())

			h, discarded, err := retention.ReadHorizons(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(MatchError(ContainSubstring(path)))
			Expect(h).To(BeEmpty())
			Expect(path).NotTo(BeAnExistingFile())
			Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte(content)))
		},
		Entry("empty", ""),
		Entry("truncated", "{"),
		Entry("a horizon that is not a time", `{"github.com/o/r":"garbage"}`),
	)

	It("are peeked at without the write lock, which skips nothing for a missing or corrupt file and leaves it in place", func() {
		s := newStore()
		Expect(retention.PeekHorizons(s.State())).To(BeEmpty())
		Expect(retention.Horizons{repo: at}.Write(s)).To(Succeed())
		Expect(retention.PeekHorizons(s.State())).To(Equal(retention.Horizons{repo: at}))

		path := filepath.Join(s.State(), "horizon.json")
		Expect(os.WriteFile(path, []byte("{"), 0o644)).To(Succeed())
		Expect(retention.PeekHorizons(s.State())).To(BeEmpty())
		Expect(path).To(BeARegularFile())
	})
})

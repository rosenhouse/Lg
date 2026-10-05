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

var _ = Describe("retention.Horizon", Label("retention"), func() {
	at := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)

	It("skips nothing when state/horizon.json is missing", func() {
		h, discarded, err := retention.ReadHorizon(newStore())
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
		Expect(h.Skips(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))).To(BeFalse())
	})

	It("is written to state/horizon.json and read back, and skips runs created at or before it", func() {
		s := newStore()
		Expect(retention.Horizon{At: at}.Write(s)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(s.State(), "horizon.json"))).To(MatchJSON(`{"horizon":"2026-09-30T18:00:00Z"}`))

		h, discarded, err := retention.ReadHorizon(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(discarded).NotTo(HaveOccurred())
		Expect(h.At).To(BeTemporally("==", at))
		Expect(h.Skips(at)).To(BeTrue())
		Expect(h.Skips(at.Add(-time.Second))).To(BeTrue())
		Expect(h.Skips(at.Add(time.Second))).To(BeFalse())
	})

	DescribeTable("moves state/horizon.json aside when it does not parse, and skips nothing",
		func(content string) {
			s := newStore()
			path := filepath.Join(s.State(), "horizon.json")
			Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())

			h, discarded, err := retention.ReadHorizon(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(discarded).To(MatchError(ContainSubstring(path)))
			Expect(h).To(BeZero())
			Expect(path).NotTo(BeAnExistingFile())
			Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte(content)))
		},
		Entry("empty", ""),
		Entry("truncated", "{"),
		Entry("a horizon that is not a time", `{"horizon":"garbage"}`),
	)
})

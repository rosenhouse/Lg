package recordings_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("RecordedAt", Label("artifacts"), func() {
	It("reads a stage's recorded_at.txt as UTC", func() {
		Expect(recordings.RecordedAt(37129390741, "after-expiry")).To(Equal(time.Date(2026, 10, 4, 16, 29, 6, 0, time.UTC)))
		Expect(recordings.RecordedAt(37129738159, "after-expiry")).To(Equal(time.Date(2026, 10, 4, 16, 30, 1, 0, time.UTC)))
	})

	It("fails for a stage with no recorded_at.txt", func() {
		_, err := recordings.RecordedAt(37129390741, "after-attempt-3")
		Expect(err).To(MatchError(ContainSubstring("recorded_at.txt")))
	})
})

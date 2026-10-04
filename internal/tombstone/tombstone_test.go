package tombstone_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/tombstone"
)

const logURL = "https://api.github.com/repos/o/r/actions/jobs/1/logs"

var (
	updated = time.Date(2026, 10, 3, 14, 24, 12, 0, time.UTC)
	grace   = time.Hour
)

// statusError is what github returns for a failed hop of kind.
func statusError(kind error, status int, message string) error {
	return errors.Join(kind, &github.StatusError{URL: logURL, Status: status, Message: message})
}

func asJSON(t tombstone.Tombstone) map[string]any {
	GinkgoHelper()
	raw, err := json.Marshal(t)
	Expect(err).NotTo(HaveOccurred())
	var fields map[string]any
	Expect(json.Unmarshal(raw, &fields)).To(Succeed())
	return fields
}

var _ = Describe("Tombstone JSON", Label("failures"), func() {
	It("has lg_format, tombstoned_at, target, url, http_status, reason and message", func() {
		t, ok := tombstone.FromError(statusError(github.ErrGone, 410, "Gone"), updated, grace, updated.Add(time.Minute+500*time.Millisecond))
		Expect(ok).To(BeTrue())
		Expect(asJSON(t)).To(Equal(map[string]any{
			"lg_format":     1.0,
			"tombstoned_at": "2026-10-03T14:25:12Z",
			"target":        "log.txt",
			"url":           logURL,
			"http_status":   410.0,
			"reason":        "expired",
			"message":       "Gone",
		}))
	})

	It("has a null http_status for not_applicable", func() {
		t := tombstone.NotApplicable("log.txt", logURL, "no steps and no runner", updated)
		Expect(asJSON(t)).To(Equal(map[string]any{
			"lg_format":     1.0,
			"tombstoned_at": "2026-10-03T14:24:12Z",
			"target":        "log.txt",
			"url":           logURL,
			"http_status":   nil,
			"reason":        "not_applicable",
			"message":       "no steps and no runner",
		}))
	})
})

var _ = Describe("FromError", Label("failures"), func() {
	DescribeTable("tombstones a log GitHub has lost for good",
		func(err error, now time.Time, reason tombstone.Reason, status int) {
			t, ok := tombstone.FromError(err, updated, grace, now)
			Expect(ok).To(BeTrue())
			Expect(t.Reason).To(Equal(reason))
			Expect(t.HTTPStatus).To(HaveValue(Equal(status)))
			Expect(t.TombstonedAt).To(Equal(now))
		},
		Entry("ErrNotFound after log_grace is deleted", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrBlobMissing after log_grace is deleted", statusError(github.ErrBlobMissing, 404, "The specified blob does not exist."), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrGone within log_grace is expired", statusError(github.ErrGone, 410, "Gone"), updated, tombstone.Expired, 410),
	)

	DescribeTable("gives no tombstone for what may yet come back",
		func(err error, now time.Time) {
			_, ok := tombstone.FromError(err, updated, grace, now)
			Expect(ok).To(BeFalse())
		},
		Entry("ErrNotFound at log_grace", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(grace)),
		Entry("ErrNotFound within log_grace", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(time.Minute)),
		Entry("ErrBlobMissing at log_grace", statusError(github.ErrBlobMissing, 404, ""), updated.Add(grace)),
		Entry("a Transient 500", failure.Transient{Err: &github.StatusError{URL: logURL, Status: 500}}, updated.Add(24*time.Hour)),
		Entry("a Transient ErrNotFound", failure.Transient{Err: statusError(github.ErrNotFound, 404, "")}, updated.Add(24*time.Hour)),
		Entry("any other error", errors.New("disk full"), updated.Add(24*time.Hour)),
	)
})

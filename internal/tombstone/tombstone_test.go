package tombstone_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/tombstone"
)

const (
	logURL         = "https://api.github.com/repos/o/r/actions/jobs/1/logs"
	expiredMessage = "The logs for this run have expired and are no longer available."
)

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
		t, err := tombstone.FromError(statusError(github.ErrGone, 410, expiredMessage), updated, grace, updated.Add(time.Minute+500*time.Millisecond))
		Expect(err).NotTo(HaveOccurred())
		Expect(asJSON(t)).To(Equal(map[string]any{
			"lg_format":     1.0,
			"tombstoned_at": "2026-10-03T14:25:12Z",
			"target":        "log.txt",
			"url":           logURL,
			"http_status":   410.0,
			"reason":        "expired",
			"message":       expiredMessage,
		}))
	})

	It("has a null http_status for not_applicable", func() {
		t := tombstone.NeverProduced("log.txt", logURL, "no steps and no runner", updated)
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
		func(lost error, now time.Time, reason tombstone.Reason, status int) {
			t, err := tombstone.FromError(lost, updated, grace, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Reason).To(Equal(reason))
			Expect(t.HTTPStatus).To(HaveValue(Equal(status)))
			Expect(t.TombstonedAt).To(Equal(now))
		},
		Entry("ErrNotFound after log_grace is deleted", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrBlobMissing after log_grace is deleted", statusError(github.ErrBlobMissing, 404, "The specified blob does not exist."), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrGone within log_grace is expired", statusError(github.ErrGone, 410, "Gone"), updated, tombstone.Expired, 410),
	)

	DescribeTable("calls a 404 within log_grace Transient",
		func(lost error, now time.Time) {
			_, err := tombstone.FromError(lost, updated, grace, now)
			Expect(err).To(BeTransient())
			Expect(err).To(MatchError(lost))
			Expect(err).To(MatchError(ContainSubstring("within log_grace")))
		},
		Entry("ErrNotFound at log_grace", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(grace)),
		Entry("ErrNotFound within log_grace", statusError(github.ErrNotFound, 404, "Not Found"), updated.Add(time.Minute)),
		Entry("ErrBlobMissing at log_grace", statusError(github.ErrBlobMissing, 404, ""), updated.Add(grace)),
	)

	DescribeTable("returns any other error as it is",
		func(other error) {
			_, err := tombstone.FromError(other, updated, grace, updated.Add(24*time.Hour))
			Expect(err).To(BeIdenticalTo(other))
		},
		Entry("a Transient 500", failure.Transient{Err: &github.StatusError{URL: logURL, Status: 500}}),
		Entry("a Transient ErrNotFound", failure.Transient{Err: statusError(github.ErrNotFound, 404, "")}),
		Entry("an API 422", statusError(errors.New("unprocessable"), 422, "")),
		Entry("any other error", errors.New("disk full")),
	)
})

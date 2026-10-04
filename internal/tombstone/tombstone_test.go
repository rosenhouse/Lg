package tombstone_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/model"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
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

func apiError(status int, message string) error {
	return &github.StatusError{URL: logURL, Status: status, Message: message}
}

func blobError(status int, message string) error {
	return &github.StatusError{URL: logURL, Status: status, Message: message, Blob: true}
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
		t, err := tombstone.FromError(apiError(410, expiredMessage), updated, grace, updated.Add(time.Minute+500*time.Millisecond))
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

	It("has a null http_status for a reason of lg's own", func() {
		t := tombstone.New("log.txt", logURL, tombstone.NotApplicable, "no steps and no runner", updated)
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
		Entry("ErrNotFound after log_grace is deleted", apiError(404, "Not Found"), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrBlobMissing after log_grace is deleted", blobError(404, "The specified blob does not exist."), updated.Add(grace+time.Second), tombstone.Deleted, 404),
		Entry("ErrGone within log_grace is expired", apiError(410, "Gone"), updated, tombstone.Expired, 410),
	)

	DescribeTable("calls a 404 within log_grace Transient",
		func(lost error, now time.Time) {
			_, err := tombstone.FromError(lost, updated, grace, now)
			Expect(err).To(BeTransient())
			Expect(err).To(MatchError(lost))
			Expect(err).To(MatchError(ContainSubstring("within log_grace")))
		},
		Entry("ErrNotFound at log_grace", apiError(404, "Not Found"), updated.Add(grace)),
		Entry("ErrNotFound within log_grace", apiError(404, "Not Found"), updated.Add(time.Minute)),
		Entry("ErrBlobMissing at log_grace", blobError(404, ""), updated.Add(grace)),
	)

	DescribeTable("returns any other error as it is",
		func(other error) {
			_, err := tombstone.FromError(other, updated, grace, updated.Add(24*time.Hour))
			Expect(err).To(BeIdenticalTo(other))
		},
		Entry("a Transient 500", failure.Transient{Err: &github.StatusError{URL: logURL, Status: 500}}),
		Entry("a Transient ErrNotFound", failure.Transient{Err: apiError(404, "")}),
		Entry("an API 422", apiError(422, "")),
		Entry("a Blocked auth", failure.Blocked{Kind: failure.Auth, Detail: "401 Unauthorized"}),
		Entry("a Blocked rate_limit", failure.Blocked{Kind: failure.RateLimit, Detail: "429 Too Many Requests", RetryAt: updated.Add(time.Hour)}),
		Entry("a Blocked unreachable", failure.Blocked{Kind: failure.Unreachable, Detail: "connection refused"}),
		Entry("a Blocked local_io", failure.Blocked{Kind: failure.LocalIO, Detail: "no space left on device"}),
		Entry("any other error", errors.New("disk full")),
	)
})

// recordedArtifact is an element of a recorded artifact listing.
func recordedArtifact(runID int64, stage string, id int64) model.Artifact {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(recordings.Dir(runID, stage), "artifacts.json"))
	Expect(err).NotTo(HaveOccurred())
	var listing struct{ Artifacts []model.Artifact }
	Expect(json.Unmarshal(raw, &listing)).To(Succeed())
	i := slices.IndexFunc(listing.Artifacts, func(a model.Artifact) bool { return a.ID == id })
	Expect(i).To(BeNumerically(">=", 0), "artifact %d at %s", id, stage)
	return listing.Artifacts[i]
}

// recordedZipError is the first-hop failure that record.sh wrote for an artifact's zip.
func recordedZipError(runID int64, stage string, id int64) *github.StatusError {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(recordings.Dir(runID, stage), "artifacts", fmt.Sprintf("%d.zip", id)))
	Expect(err).NotTo(HaveOccurred())
	var body struct {
		Message string
		Status  string
	}
	Expect(json.Unmarshal(raw, &body)).To(Succeed())
	status, err := strconv.Atoi(body.Status)
	Expect(err).NotTo(HaveOccurred())
	return &github.StatusError{URL: fmt.Sprintf("https://api.github.com/repos/rosenhouse/Lg/actions/artifacts/%d/zip", id), Status: status, Message: body.Message}
}

var _ = Describe("FromZipError over the after-expiry recording of run 37129390741", Label("artifacts"), func() {
	const run = 37129390741
	var recordedAt time.Time

	BeforeEach(func() {
		var err error
		recordedAt, err = recordings.RecordedAt(run, "after-expiry")
		Expect(err).NotTo(HaveOccurred())
	})

	It("tombstones 11275917910, deleted by a re-run-all, as deleted on its 404 although its expires_at has passed", func() {
		artifact := recordedArtifact(run, "after-attempt-1", 11275917910)
		Expect(artifact.ExpiresAt).To(BeTemporally("<", recordedAt))

		t, err := tombstone.FromZipError(recordedZipError(run, "after-expiry", 11275917910), artifact.ExpiresAt, recordedAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Reason).To(Equal(tombstone.Deleted))
		Expect(t.HTTPStatus).To(HaveValue(Equal(http.StatusNotFound)))
		Expect(t.Message).To(Equal("Not Found"))
	})

	It("tombstones 11276327411 as expired on its 410", func() {
		artifact := recordedArtifact(run, "after-attempt-3", 11276327411)

		t, err := tombstone.FromZipError(recordedZipError(run, "after-expiry", 11276327411), artifact.ExpiresAt, recordedAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Reason).To(Equal(tombstone.Expired))
		Expect(t.HTTPStatus).To(HaveValue(Equal(http.StatusGone)))
		Expect(t.Message).To(Equal("Artifact has expired"))
	})
})

var _ = Describe("FromZipError", Label("artifacts"), func() {
	const zipURL = "https://api.github.com/repos/o/r/actions/artifacts/1/zip"
	expiresAt := time.Date(2026, 10, 4, 14, 23, 1, 0, time.UTC)

	DescribeTable("tombstones artifact.zip with the failed hop's status and message",
		func(lost *github.StatusError, now time.Time, reason tombstone.Reason) {
			lost.URL = zipURL
			t, err := tombstone.FromZipError(lost, expiresAt, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(asJSON(t)).To(Equal(map[string]any{
				"lg_format":     1.0,
				"tombstoned_at": now.Format(time.RFC3339),
				"target":        "artifact.zip",
				"url":           zipURL,
				"http_status":   float64(lost.Status),
				"reason":        string(reason),
				"message":       lost.Message,
			}))
		},
		Entry("410 is expired", &github.StatusError{Status: 410, Message: "Gone"}, expiresAt.Add(-time.Hour), tombstone.Expired),
		Entry("404 before expires_at is deleted", &github.StatusError{Status: 404, Message: "Not Found"}, expiresAt.Add(-time.Second), tombstone.Deleted),
		Entry("404 at expires_at is deleted", &github.StatusError{Status: 404, Message: "Not Found"}, expiresAt, tombstone.Deleted),
		Entry("404 once expires_at has passed is deleted", &github.StatusError{Status: 404, Message: "Not Found"}, expiresAt.Add(time.Second), tombstone.Deleted),
		Entry("blob 404 is deleted", &github.StatusError{Status: 404, Message: "The specified blob does not exist.", Blob: true}, expiresAt.Add(-time.Second), tombstone.Deleted),
	)

	DescribeTable("gives back any other error",
		func(other error) {
			_, err := tombstone.FromZipError(other, expiresAt, expiresAt.Add(time.Hour))
			Expect(err).To(Equal(other))
		},
		Entry("a Transient 500", failure.Transient{Err: &github.StatusError{URL: zipURL, Status: 500}}),
		Entry("a Transient ErrNotFound", failure.Transient{Err: &github.StatusError{URL: zipURL, Status: 404}}),
		Entry("an API 400", &github.StatusError{URL: zipURL, Status: 400}),
		Entry("a Blocked auth", failure.Blocked{Kind: failure.Auth, Detail: "401 Unauthorized"}),
		Entry("a Blocked rate_limit", failure.Blocked{Kind: failure.RateLimit, Detail: "429 Too Many Requests"}),
		Entry("any other error", errors.New("disk full")),
	)
})

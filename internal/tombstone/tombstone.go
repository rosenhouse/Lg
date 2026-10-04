// Package tombstone marks a file that GitHub will never provide.
package tombstone

import (
	"errors"
	"fmt"
	"time"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/model"
)

type Reason string

const (
	Expired       Reason = "expired"
	Deleted       Reason = "deleted"
	NotApplicable Reason = "not_applicable"
	TooLarge      Reason = "too_large"
)

// Tombstone is stored as <target>.tombstone in place of target.
type Tombstone struct {
	LgFormat     int       `json:"lg_format"`
	TombstonedAt time.Time `json:"tombstoned_at"`
	Target       string    `json:"target"`
	URL          string    `json:"url"`
	HTTPStatus   *int      `json:"http_status"`
	Reason       Reason    `json:"reason"`
	Message      string    `json:"message"`
}

// New tombstones a target for a reason of lg's own, with no HTTP status.
func New(target, url string, reason Reason, message string, now time.Time) Tombstone {
	return Tombstone{LgFormat: 1, TombstonedAt: now.UTC().Truncate(time.Second), Target: target, URL: url, Reason: reason, Message: message}
}

// FromError tombstones log.txt when err shows GitHub has lost it for good: a
// 410, or a 404 once log_grace has passed since the attempt's updated_at. A
// 404 within log_grace is Transient. Any other error comes back as it is.
func FromError(err error, attemptUpdatedAt time.Time, logGrace time.Duration, now time.Time) (Tombstone, error) {
	statusErr, ok := permanent(err)
	if !ok {
		return Tombstone{}, err
	}
	var reason Reason
	switch {
	case errors.Is(err, github.ErrGone):
		reason = Expired
	case errors.Is(err, github.ErrNotFound), errors.Is(err, github.ErrBlobMissing):
		if !now.After(attemptUpdatedAt.Add(logGrace)) {
			return Tombstone{}, failure.Transient{Err: fmt.Errorf("within log_grace: %w", err)}
		}
		reason = Deleted
	default:
		return Tombstone{}, err
	}
	return fromStatus("log.txt", statusErr, reason, now), nil
}

// FromZipError tombstones artifact.zip when err shows GitHub has lost it for
// good: a 410, or a 404, which is expired once expires_at has passed and
// deleted before it or with no expires_at. It gives back any other err.
func FromZipError(err error, artifact model.Artifact, logGrace time.Duration, now time.Time) (Tombstone, error) {
	expiresAt := artifact.ExpiresAt
	statusErr, ok := permanent(err)
	if !ok {
		return Tombstone{}, err
	}
	notFound := errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrBlobMissing)
	expired := !expiresAt.IsZero() && !expiresAt.After(now)
	var reason Reason
	switch {
	case errors.Is(err, github.ErrGone), notFound && expired:
		reason = Expired
	case notFound:
		reason = Deleted
	default:
		return Tombstone{}, err
	}
	return fromStatus("artifact.zip", statusErr, reason, now), nil
}

// permanent gives the StatusError of err unless err is Transient.
func permanent(err error) (*github.StatusError, bool) {
	var transient failure.Transient
	var statusErr *github.StatusError
	if errors.As(err, &transient) || !errors.As(err, &statusErr) {
		return nil, false
	}
	return statusErr, true
}

func fromStatus(target string, statusErr *github.StatusError, reason Reason, now time.Time) Tombstone {
	t := New(target, statusErr.URL, reason, statusErr.Message, now)
	t.HTTPStatus = &statusErr.Status
	return t
}

// Package failure says what a sync cycle does after an error.
package failure

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Transient aborts only the unit it happened in. The next cycle retries the unit.
type Transient struct{ Err error }

func (t Transient) Error() string { return t.Err.Error() }

func (t Transient) Unwrap() error { return t.Err }

type Kind string

const (
	Auth        Kind = "auth"
	RateLimit   Kind = "rate_limit"
	Unreachable Kind = "unreachable"
	LocalIO     Kind = "local_io"
)

// Blocked stops the cycle: no unit can make progress until its cause is fixed or RetryAt passes.
type Blocked struct {
	Kind    Kind
	Detail  string
	RetryAt time.Time
}

func (b Blocked) Error() string {
	if b.RetryAt.IsZero() {
		return fmt.Sprintf("blocked (%s): %s", b.Kind, b.Detail)
	}
	return fmt.Sprintf("blocked (%s, retry_at %s): %s", b.Kind, b.RetryAt.UTC().Format(time.RFC3339), b.Detail)
}

// FromStatus classifies an API response that refuses lg's credentials or
// rate by its status, headers and GitHub's message.
func FromStatus(status int, header http.Header, message, detail string, now time.Time) (Blocked, bool) {
	rateLimited := status == http.StatusTooManyRequests || (status == http.StatusForbidden &&
		(header.Get("Retry-After") != "" || header.Get("X-RateLimit-Remaining") == "0" ||
			strings.Contains(strings.ToLower(message), "secondary rate limit")))
	switch {
	case rateLimited:
		return Blocked{Kind: RateLimit, Detail: detail, RetryAt: retryAt(header, now)}, true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Blocked{Kind: Auth, Detail: detail}, true
	}
	return Blocked{}, false
}

// maxRetryAfter caps a Retry-After that GitHub would never send.
const maxRetryAfter = 24 * time.Hour

// retryAt follows GitHub's advice: wait Retry-After seconds, else until the
// reset when none remain, else a minute.
func retryAt(header http.Header, now time.Time) time.Time {
	if seconds, err := strconv.ParseUint(header.Get("Retry-After"), 10, 64); err == nil {
		return now.Add(time.Duration(min(seconds, uint64(maxRetryAfter.Seconds()))) * time.Second)
	}
	if reset, ok := resetOf(header); ok && header.Get("X-RateLimit-Remaining") == "0" {
		return reset
	}
	return now.Add(time.Minute)
}

func resetOf(header http.Header) (time.Time, bool) {
	unix, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64)
	return time.Unix(unix, 0).UTC(), err == nil
}

// Reserve blocks until the reset once fewer than 10% of the rate limit's
// requests remain, keeping the rest for the user's own use of the token.
func Reserve(header http.Header, received, now time.Time) (Blocked, bool) {
	limit, errLimit := strconv.Atoi(header.Get("X-RateLimit-Limit"))
	remaining, errRemaining := strconv.Atoi(header.Get("X-RateLimit-Remaining"))
	reset, ok := resetOf(header)
	if errLimit != nil || errRemaining != nil || !ok || remaining*10 >= limit || !now.Before(reset) {
		return Blocked{}, false
	}
	detail := fmt.Sprintf("X-RateLimit-Remaining %d is below 10%% of X-RateLimit-Limit %d", remaining, limit)
	return Blocked{Kind: RateLimit, Detail: detail, RetryAt: reset}, true
}

// FromErrno blocks on a local error that no retry fixes: a full, read-only or
// unwritable store, or a tmp/ that cannot be renamed into data/. It returns
// any other err as it is.
func FromErrno(err error) error {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EDQUOT, syscall.EROFS, syscall.EACCES, syscall.EXDEV} {
		if errors.Is(err, errno) {
			return Blocked{Kind: LocalIO, Detail: err.Error()}
		}
	}
	return err
}

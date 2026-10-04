// Package failure says what a sync cycle does after an error.
package failure

import "time"

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

func (b Blocked) Error() string { return "" }

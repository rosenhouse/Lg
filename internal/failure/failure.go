// Package failure says what a sync cycle does after an error.
package failure

// Transient aborts only the unit it happened in. The next cycle retries the unit.
type Transient struct{ Err error }

func (t Transient) Error() string { return t.Err.Error() }

func (t Transient) Unwrap() error { return t.Err }

// Package matchers holds Gomega matchers that several suites share.
package matchers

import (
	"errors"

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/failure"
)

func BeTransient() types.GomegaMatcher {
	return gomega.MatchError(func(err error) bool {
		var transient failure.Transient
		return errors.As(err, &transient)
	}, "wraps a failure.Transient")
}

// Package matchers holds Gomega matchers that several suites share.
package matchers

import (
	"errors"
	"fmt"

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

// BeBlocked matches an error that wraps a failure.Blocked of kind whose
// fields satisfy every one of fields.
func BeBlocked(kind failure.Kind, fields ...types.GomegaMatcher) types.GomegaMatcher {
	return gomega.WithTransform(func(err error) (failure.Blocked, error) {
		var blocked failure.Blocked
		if !errors.As(err, &blocked) {
			return blocked, fmt.Errorf("want a failure.Blocked, got %v", err)
		}
		return blocked, nil
	}, gomega.SatisfyAll(append([]types.GomegaMatcher{gomega.HaveField("Kind", kind)}, fields...)...))
}

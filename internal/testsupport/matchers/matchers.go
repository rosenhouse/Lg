// Package matchers holds Gomega matchers that several suites share.
package matchers

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

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
			return blocked, errors.New("want a failure.Blocked, got " + fmt.Sprint(err))
		}
		return blocked, nil
	}, gomega.SatisfyAll(append([]types.GomegaMatcher{gomega.HaveField("Kind", kind)}, fields...)...))
}

// BeSwept matches a store's tmp/ dir that holds nothing but an empty trash/.
func BeSwept() types.GomegaMatcher {
	return gomega.WithTransform(func(tmp string) ([]string, error) {
		var left []string
		err := filepath.WalkDir(tmp, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if rel, _ := filepath.Rel(tmp, path); rel != "." && (rel != "trash" || !d.IsDir()) {
				left = append(left, rel)
			}
			return nil
		})
		return left, err
	}, gomega.BeEmpty())
}

package mirror_test

import (
	"errors"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
)

var _ = DescribeTable("RunScoped", Label("failures"),
	func(err error, scoped bool) {
		Expect(mirror.RunScoped(err)).To(Equal(scoped))
	},
	Entry("a Transient error", failure.Transient{Err: errors.New("connection reset")}, true),
	Entry("a blob 401", failure.Transient{Err: &github.StatusError{Status: 401, Blob: true}}, true),
	Entry("a blob 403", failure.Transient{Err: &github.StatusError{Status: 403, Blob: true}}, true),
	Entry("a blob 429", failure.Transient{Err: &github.StatusError{Status: 429, Blob: true}}, true),
	Entry("an API 400", &github.StatusError{Status: 400}, true),
	Entry("a malformed body", &github.MalformedError{Err: errors.New("invalid character")}, true),
	Entry("an API 401", &github.StatusError{Status: 401}, false),
	Entry("an API 403", &github.StatusError{Status: 403}, false),
	Entry("an API 429", &github.StatusError{Status: 429}, false),
	Entry("a local error", syscall.ENOSPC, false),
)

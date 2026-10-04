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
	Entry("a Blocked error", failure.Blocked{Kind: failure.RateLimit}, false),
	Entry("a Transient error joined with a Blocked one", errors.Join(failure.Transient{Err: errors.New("connection reset")}, failure.Blocked{Kind: failure.Auth}), false),
	Entry("a local error", syscall.ENOSPC, false),
	Entry("a Transient error joined with a local one", errors.Join(failure.Transient{Err: errors.New("connection reset")}, syscall.ENOSPC), false),
	Entry("a local error joined with a Transient one", errors.Join(syscall.EIO, failure.Transient{Err: errors.New("connection reset")}), false),
	Entry("a Transient error joined with an API 400", errors.Join(failure.Transient{Err: errors.New("connection reset")}, &github.StatusError{Status: 400}), true),
)

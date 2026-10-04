package main_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

func TestFakegithub(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fakegithub Dev Server Suite")
}

var serverPath string

var _ = SynchronizedBeforeSuite(func() []byte {
	path, err := gexec.Build("github.com/rosenhouse/lg/internal/testsupport/cmd/fakegithub")
	Expect(err).NotTo(HaveOccurred())
	return []byte(path)
}, func(path []byte) {
	serverPath = string(path)
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	gexec.CleanupBuildArtifacts()
})

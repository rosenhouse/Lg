package e2e_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var lgPath string

var _ = SynchronizedBeforeSuite(func() []byte {
	path, err := gexec.Build("github.com/rosenhouse/lg/cmd/lg",
		"-race", "-ldflags", "-X github.com/rosenhouse/lg/internal/version.Version=test")
	Expect(err).NotTo(HaveOccurred())
	return []byte(path)
}, func(path []byte) {
	lgPath = string(path)
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	gexec.CleanupBuildArtifacts()
})

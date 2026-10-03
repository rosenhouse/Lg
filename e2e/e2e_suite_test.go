package e2e_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var lgPath, unstampedLgPath string

var _ = SynchronizedBeforeSuite(func() []byte {
	stamped, err := gexec.Build("github.com/rosenhouse/lg/cmd/lg",
		"-race", "-ldflags", "-X github.com/rosenhouse/lg/internal/version.Version=test")
	Expect(err).NotTo(HaveOccurred())
	unstamped, err := gexec.Build("github.com/rosenhouse/lg/cmd/lg")
	Expect(err).NotTo(HaveOccurred())
	return []byte(stamped + "\x00" + unstamped)
}, func(paths []byte) {
	lgPath, unstampedLgPath, _ = strings.Cut(string(paths), "\x00")
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	gexec.CleanupBuildArtifacts()
})

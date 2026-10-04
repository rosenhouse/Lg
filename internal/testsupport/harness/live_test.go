package harness_test

import (
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("NewLive", Label("cli"), func() {
	It("sets LG_GH to the real gh, gives the spec its own LG_HOME and LG_CONFIG, and leaves LG_TEST_NOW unset", func() {
		env := harness.NewLive("/bin/lg")

		gh, err := exec.LookPath("gh")
		Expect(err).NotTo(HaveOccurred())
		Expect(env.Getenv("LG_GH")).To(Equal(gh))
		Expect(env.Getenv("LG_TEST_NOW")).To(BeEmpty())
		Expect(env.Store()).To(Equal(env.Getenv("LG_HOME")))
		Expect(env.Store()).To(BeADirectory())
		Expect(env.ConfigFile()).To(Equal(env.Getenv("LG_CONFIG")))
		Expect(filepath.Dir(env.ConfigFile())).To(BeADirectory())
		Expect(env.ConfigFile()).NotTo(BeAnExistingFile())
	})
})

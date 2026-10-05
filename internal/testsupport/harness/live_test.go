package harness_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("NewLive", Label("cli"), func() {
	var binDir string

	BeforeEach(func() {
		binDir = GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(binDir, "gh"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		GinkgoT().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	})

	It("sets LG_GH to the first gh on PATH, gives the spec its own LG_HOME and LG_CONFIG, and leaves LG_TEST_NOW unset", func() {
		GinkgoT().Setenv("LG_HOME", "/caller")
		GinkgoT().Setenv("LG_TEST_NOW", "2026-10-03T18:00:00Z")

		env := harness.NewLive("/bin/lg")

		Expect(env.Getenv("LG_GH")).To(Equal(filepath.Join(binDir, "gh")))
		Expect(env.Getenv("LG_TEST_NOW")).To(BeEmpty())
		Expect(env.Store()).To(Equal(env.Getenv("LG_HOME")))
		Expect(env.Store()).To(BeADirectory())
		Expect(env.ConfigFile()).To(Equal(env.Getenv("LG_CONFIG")))
		Expect(filepath.Dir(env.ConfigFile())).To(BeADirectory())
		Expect(env.ConfigFile()).NotTo(BeAnExistingFile())
	})

	It("keeps the proxy variables, GH_TOKEN and GH_CONFIG_DIR, and drops the other GH_ and XDG_ variables", func() {
		GinkgoT().Setenv("HTTPS_PROXY", "P")
		GinkgoT().Setenv("https_proxy", "p")
		GinkgoT().Setenv("NO_PROXY", "N")
		GinkgoT().Setenv("no_proxy", "n")
		GinkgoT().Setenv("GH_TOKEN", "t")
		GinkgoT().Setenv("GH_CONFIG_DIR", "/gh")
		GinkgoT().Setenv("GH_HOST", "h")
		GinkgoT().Setenv("XDG_DATA_HOME", "/xdg")

		env := harness.NewLive("/bin/lg")

		Expect(env.Getenv("HTTPS_PROXY")).To(Equal("P"))
		Expect(env.Getenv("https_proxy")).To(Equal("p"))
		Expect(env.Getenv("NO_PROXY")).To(Equal("N"))
		Expect(env.Getenv("no_proxy")).To(Equal("n"))
		Expect(env.Getenv("GH_TOKEN")).To(Equal("t"))
		Expect(env.Getenv("GH_CONFIG_DIR")).To(Equal("/gh"))
		Expect(env.Getenv("GH_HOST")).To(BeEmpty())
		Expect(env.Getenv("XDG_DATA_HOME")).To(BeEmpty())
	})
})

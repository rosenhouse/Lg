package fakegh_test

import (
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
)

var _ = Describe("fake gh", Label("transport"), func() {
	It("prints lg-test-token and logs the arguments of each run", func() {
		gh := fakegh.New(GinkgoT().TempDir())

		for range 2 {
			out, err := exec.CommandContext(GinkgoT().Context(), gh.Path, "auth", "token", "--hostname", "github.com").Output()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out)).To(Equal("lg-test-token\n"))
		}
		Expect(gh.Calls()).To(Equal([]string{
			"auth token --hostname github.com",
			"auth token --hostname github.com",
		}))
	})

	It("prints the token SetToken gives it", func() {
		gh := fakegh.New(GinkgoT().TempDir())

		gh.SetToken("gho_rewritten")

		out, err := exec.CommandContext(GinkgoT().Context(), gh.Path, "auth", "token").Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal("gho_rewritten\n"))
	})

	It("logs the GH_CONFIG_DIR of each run", func() {
		gh := fakegh.New(GinkgoT().TempDir())

		for _, dir := range []string{"/gh/one", ""} {
			cmd := exec.CommandContext(GinkgoT().Context(), gh.Path, "auth", "token")
			cmd.Env = []string{"GH_CONFIG_DIR=" + dir}
			Expect(cmd.Run()).To(Succeed())
		}
		Expect(gh.ConfigDirs()).To(Equal([]string{"/gh/one", ""}))
	})
})

package fakegh_test

import (
	"bytes"
	"context"
	"os/exec"
	"time"

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

var _ = Describe("fake gh told to Fail", Label("blocked"), func() {
	It("prints the given stderr, no token, and exits 1", func() {
		gh := fakegh.New(GinkgoT().TempDir())
		gh.Fail("no oauth token found for github.com")

		cmd := exec.CommandContext(GinkgoT().Context(), gh.Path, "auth", "token")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		Expect(cmd.Run()).To(MatchError("exit status 1"))
		Expect(stderr.String()).To(Equal("no oauth token found for github.com\n"))
		Expect(stdout.String()).To(BeEmpty())
		Expect(gh.Calls()).To(Equal([]string{"auth token"}))
	})
})

var _ = Describe("fake gh told to Hang", Label("blocked"), func() {
	It("prints nothing until it is killed", func(ctx SpecContext) {
		gh := fakegh.New(GinkgoT().TempDir())
		gh.Hang()

		timeout, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(timeout, gh.Path, "auth", "token")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		Expect(cmd.Run()).To(MatchError("signal: killed"))
		Expect(stdout.String()).To(BeEmpty())
		Expect(gh.Calls()).To(Equal([]string{"auth token"}))
	}, SpecTimeout(5*time.Second))
})

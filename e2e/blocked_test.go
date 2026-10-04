package e2e_test

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync when gh fails", Label("blocked"), func() {
	It("exits 3, prints gh's stderr and a `gh auth login --hostname github.com` hint, and sends no request", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		env.GH().Fail("no oauth token found for github.com")

		session := env.Sync()
		Expect(session).To(gexec.Exit(3))
		Expect(session.Err).To(gbytes.Say("no oauth token found for github.com"))
		Expect(session.Err).To(gbytes.Say("gh auth login --hostname github.com"))
		Expect(fake.Requests()).To(BeEmpty())
	})
})

var _ = Describe("lg sync interrupted while gh hangs", Label("blocked"), func() {
	It("leaves no gh running", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		env.GH().Hang()

		session, wait := env.StartSync()
		Eventually(env.GH().HungPIDs, harness.ExitTimeout).Should(HaveLen(1))
		gh := env.GH().HungPIDs()[0]
		DeferCleanup(func() { _ = syscall.Kill(gh, syscall.SIGKILL) })
		session.Interrupt()

		Expect(wait()).NotTo(gexec.Exit(0))
		Eventually(func() error { return syscall.Kill(gh, 0) }, harness.ExitTimeout).Should(MatchError(syscall.ESRCH))
	})
})

var _ = Describe("lg sync when GET /repos/rosenhouse/lg returns 404", Label("blocked"), func() {
	It("exits 3 saying the repo was not found or the token lacks access", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		fake.Fail("api", "/repos/rosenhouse/lg", fakegithub.Fault{Status: http.StatusNotFound})

		session := env.Sync()
		Expect(session).To(gexec.Exit(3))
		Expect(session.Err).To(gbytes.Say("rosenhouse/lg was not found, or the token lacks access to it"))
	})
})

var _ = Describe("lg sync rate limited with LG_TEST_NOW one year after the recordings", Label("blocked"), func() {
	It("exits 3 printing a retry_at 60s after LG_TEST_NOW", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		yearLater := harness.DefaultNow().AddDate(1, 0, 0)
		env.SetNow(yearLater, fake)
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusTooManyRequests})

		session := env.Sync()
		Expect(session).To(gexec.Exit(3))
		match := regexp.MustCompile(`retry_at (\S+)\)`).FindSubmatch(session.Err.Contents())
		Expect(match).NotTo(BeNil(), "stderr names no retry_at")
		retryAt, err := time.Parse(time.RFC3339, string(match[1]))
		Expect(err).NotTo(HaveOccurred())
		Expect(retryAt).To(BeTemporally("~", yearLater.Add(time.Minute), harness.ExitTimeout))
	})
})

var _ = Describe("lg sync", Label("blocked"), func() {
	It("leaves the gh token nowhere under LG_HOME and in no output after a successful, a failed and a blocked sync", func() {
		const token = "gho_leakcheck_0123456789abcdef"
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		env.GH().SetToken(token)
		fake.RequireToken(token)

		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusBadGateway, Times: 1})
		failed := env.Sync()
		succeeded := env.Sync()
		fake.RequireToken("gho_another_token")
		blocked := env.Sync()

		Expect(failed).To(gexec.Exit(1))
		Expect(succeeded).To(gexec.Exit(0))
		Expect(blocked).To(gexec.Exit(3))
		for _, session := range []*gexec.Session{failed, succeeded, blocked} {
			Expect(string(session.Out.Contents())).NotTo(ContainSubstring(token))
			Expect(string(session.Err.Contents())).NotTo(ContainSubstring(token))
		}
		files := 0
		Expect(filepath.WalkDir(env.Store(), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			files++
			content, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).NotTo(ContainSubstring(token), path)
			return nil
		})).To(Succeed())
		Expect(files).To(BeNumerically(">", 20))
	})

	It("names the repo dir rosenhouse/Lg from GET /repos when the config says rosenhouse/lg", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(fake.Requests()).To(ContainElement(HaveField("Path", "/repos/rosenhouse/lg")))
		Expect(os.ReadDir(filepath.Join(env.Data(), "github.com", "rosenhouse"))).To(ConsistOf(HaveField("Name()", "Lg")))
	})
})

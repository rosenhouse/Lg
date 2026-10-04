package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync", Label("transport"), func() {
	var (
		env  *harness.Env
		fake *fakegithub.Server
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.Start(fixtureRun, "after-attempt-1")
		fake.RequireToken(fakegh.Token)
		env.WriteConfig(fake.URL())
	})

	It("sends Authorization: Bearer with the token printed by `gh auth token --hostname github.com`", func() {
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(env.GH().Calls()).To(Equal([]string{"auth token --hostname github.com"}))
		Expect(fake.Requests()).To(ContainElement(HaveField("Host", "api")))
		for _, r := range fake.Requests() {
			if r.Host == "api" {
				Expect(r.Authorization).To(BeTrue(), r.Path)
			}
		}
	})

	It("asks gh for the token of the configured host", func() {
		env.WriteConfig(fake.URL(), "host: ghe.corp.example")

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(env.GH().Calls()).To(Equal([]string{"auth token --hostname ghe.corp.example"}))
	})

	It("never sends Authorization to the blob host", func() {
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(fake.Requests()).To(ContainElement(HaveField("Host", "blob")))
		Expect(fake.Requests()).NotTo(ContainElement(SatisfyAll(
			HaveField("Host", "blob"),
			HaveField("Authorization", true),
		)))
	})
})

var _ = Describe("lg sync with listings capped at 5 per page", Label("transport"), func() {
	It("writes jobs.json with all 12 jobs and 12 job dirs", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		fake.SetPageCap(5)
		env.WriteConfig(fake.URL())

		Expect(env.Sync()).To(gexec.Exit(0))
		attempt1 := filepath.Join(env.Data(), fixtureRunDir, "attempt-1")
		var jobs []any
		raw, err := os.ReadFile(filepath.Join(attempt1, "jobs.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &jobs)).To(Succeed())
		Expect(jobs).To(HaveLen(12))
		Expect(os.ReadDir(filepath.Join(attempt1, "jobs"))).To(HaveLen(12))
	})
})

var _ = Describe("lg sync with api_url http://<fake>/api/v3", Label("transport"), func() {
	It("mirrors run 37129390741", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL() + "/api/v3")

		Expect(env.Sync()).To(gexec.Exit(0))
		logs, err := filepath.Glob(filepath.Join(env.Data(), fixtureRunDir, "attempt-1/jobs/*/log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(10))
		Expect(fake.Requests()).To(HaveEach(Or(
			HaveField("Host", "blob"),
			HaveField("Path", HavePrefix("/api/v3/")),
		)))
	})
})

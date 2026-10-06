package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg where", Label("where"), func() {
	var (
		c       *harness.CLI
		attempt string
		job     string
	)

	BeforeEach(func() {
		c = harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		attempt = filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture", "attempt-1")
		job = filepath.Join(attempt, "jobs", "111221289888_flaky")
	})

	// setHTMLURL rewrites the html_url of a JSON file under data/.
	setHTMLURL := func(path, url string) {
		GinkgoHelper()
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var fields map[string]any
		Expect(json.Unmarshal(raw, &fields)).To(Succeed())
		fields["html_url"] = url
		raw, err = json.Marshal(fields)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())
	}

	It("takes html_url from job.json, or else attempt.json", func() {
		setHTMLURL(filepath.Join(job, "job.json"), "https://ghes.example/job")
		setHTMLURL(filepath.Join(attempt, "attempt.json"), "https://ghes.example/run")

		Expect(c.Main("where", filepath.Join(job, "log.txt")+":3:x", filepath.Join(attempt, "fetch.json"))).To(Equal(0), c.Stderr.String())
		lines := strings.Split(strings.TrimSuffix(c.Stdout.String(), "\n"), "\n")
		Expect(lines).To(HaveExactElements(
			MatchRegexp(`"html_url":"https://ghes.example/job"`),
			MatchRegexp(`"html_url":"https://ghes.example/run"`),
		))
	})

	It("exits 1 naming each input it cannot decode, after printing the others", func() {
		missing := filepath.Join(c.Home, "data", "missing.txt") + ":1:x"
		host := filepath.Join(c.Home, "data", "github.com")

		Expect(c.Main("where", missing, filepath.Join(job, "log.txt"), host)).To(Equal(1))
		Expect(strings.Count(c.Stdout.String(), "\n")).To(Equal(1))
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(missing+" names no file"),
			ContainSubstring("github.com is not in a run dir"),
		))
	})

	It("exits 1 naming the run when the files of the unit holding the path do not parse", func() {
		Expect(os.WriteFile(filepath.Join(attempt, "jobs.json"), []byte("{"), 0o644)).To(Succeed())

		Expect(c.Main("where", filepath.Join(job, "log.txt"))).To(Equal(1))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(ContainSubstring(filepath.Dir(attempt) + ": cannot read its job 111221289888_flaky"))
	})

	It("gives a carried-forward job's original log only while it exists", func() {
		Expect(c.Fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))
		carried := filepath.Join(filepath.Dir(attempt), "attempt-2", "jobs", "111221662305_build-ubuntu-latest-1.23", "job.json")
		original := filepath.Join(attempt, "jobs", "111221289911_build-ubuntu-latest-1.23", "log.txt")

		Expect(c.Main("where", carried)).To(Equal(0))
		Expect(c.Stdout.String()).To(ContainSubstring(`"original_log":"` + original + `"`))
		Expect(os.Remove(original)).To(Succeed())
		Expect(c.Main("where", carried)).To(Equal(0))
		Expect(c.Stdout.String()).To(SatisfyAll(ContainSubstring(`"original_job_id":111221289911`), Not(ContainSubstring("original_log"))))
	})
})

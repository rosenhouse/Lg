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
})

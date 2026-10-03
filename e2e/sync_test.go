package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

const fixtureRun = 37129390741

var notApplicableJobs = []string{"111221290616", "111221313824"}

var _ = Describe("lg sync against run-37129390741/after-attempt-1", Label("sync"), func() {
	var (
		env       *harness.Env
		fake      *fakegithub.Server
		recording string
		attempt1  string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		fake.Load(fixtureRun, "after-attempt-1")
		recording = fakegithub.Recording(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())

		Eventually(env.Lg("sync"), harness.ExitTimeout).Should(gexec.Exit(0))
		attempt1 = filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture/attempt-1")
	})

	It("publishes attempt-1 under data/github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture", func() {
		Expect(attempt1).To(BeADirectory())
	})

	It("writes log.txt byte-identical to the recording, BOM included, for each of the 10 jobs that ran", func() {
		recorded, err := filepath.Glob(filepath.Join(recording, "attempt-1/logs/*.txt"))
		Expect(err).NotTo(HaveOccurred())
		ran := 0
		for _, rec := range recorded {
			id := strings.TrimSuffix(filepath.Base(rec), ".txt")
			if slices.Contains(notApplicableJobs, id) {
				continue
			}
			ran++
			want, err := os.ReadFile(rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(want).To(HavePrefix("\xef\xbb\xbf"))
			Expect(os.ReadFile(jobDir(attempt1, id)+"/log.txt")).To(Equal(want), "job %s", id)
		}
		Expect(ran).To(Equal(10))
	})

	It(`writes attempt.json, jobs.json (an array of 12) and each job.json JSON-equal to the compact bodies served, indented two spaces and ending in a newline, so rg -l '"head_sha": "1a51097' finds attempt.json`, func() {
		Expect(os.ReadFile(filepath.Join(attempt1, "attempt.json"))).To(Equal(indented(compact(readFile(recording, "attempt-1/attempt.json")))))

		var listing struct{ Jobs []json.RawMessage }
		Expect(json.Unmarshal(readFile(recording, "attempt-1/jobs.json"), &listing)).To(Succeed())
		Expect(listing.Jobs).To(HaveLen(12))
		served := make([][]byte, len(listing.Jobs))
		for i, job := range listing.Jobs {
			served[i] = compact(job)
		}
		jobsArray := append(append([]byte("["), bytes.Join(served, []byte(","))...), ']')
		Expect(os.ReadFile(filepath.Join(attempt1, "jobs.json"))).To(Equal(indented(jobsArray)))

		for _, job := range served {
			var j struct{ ID int64 }
			Expect(json.Unmarshal(job, &j)).To(Succeed())
			Expect(os.ReadFile(jobDir(attempt1, strconv.FormatInt(j.ID, 10)) + "/job.json")).To(Equal(indented(job)))
		}

		rg := env.Sh(`rg -l '"head_sha": "1a51097' "$1"` + " sh " + env.Data())
		Eventually(rg, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(strings.Split(string(rg.Out.Contents()), "\n")).To(ContainElement(filepath.Join(attempt1, "attempt.json")))
	})

	It("writes not_applicable log.txt.tombstone files for 111221290616 and 111221313824 and requests neither log", func() {
		for _, id := range notApplicableJobs {
			dir := jobDir(attempt1, id)
			Expect(filepath.Join(dir, "log.txt")).NotTo(BeAnExistingFile())
			Expect(os.ReadFile(filepath.Join(dir, "log.txt.tombstone"))).To(MatchJSON(
				`{"lg_format": 1, "target": "log.txt", "http_status": null, "reason": "not_applicable"}`))
		}
		Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", HaveSuffix("/jobs/111221290616/logs"))))
		Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", HaveSuffix("/jobs/111221313824/logs"))))
	})
})

// jobDir finds attempt-N/jobs/<id>_<slug> whatever the slug.
func jobDir(attemptDir, id string) string {
	GinkgoHelper()
	matches, err := filepath.Glob(filepath.Join(attemptDir, "jobs", id+"_*"))
	Expect(err).NotTo(HaveOccurred())
	Expect(matches).To(HaveLen(1), "job dir for %s", id)
	return matches[0]
}

func readFile(elem ...string) []byte {
	GinkgoHelper()
	b, err := os.ReadFile(filepath.Join(elem...))
	Expect(err).NotTo(HaveOccurred())
	return b
}

func compact(b []byte) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(json.Compact(&buf, b)).To(Succeed())
	return buf.Bytes()
}

func indented(b []byte) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(json.Indent(&buf, b, "", "  ")).To(Succeed())
	return append(buf.Bytes(), '\n')
}

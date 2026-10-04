package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync of run-37129738159/logs-deleted past log_grace", Label("failures"), func() {
	It("writes deleted log tombstones with http_status 404 and GitHub's message for the 10 jobs that ran, and not_applicable tombstones for 111222300561 and 111222327556 without requesting their logs", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(37129738159, "logs-deleted")
		env.WriteConfig(fake.URL())

		Expect(env.Sync()).To(gexec.Exit(0))
		attempts, err := filepath.Glob(filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-10-03/37129738159_*/attempt-1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts).To(HaveLen(1))
		notApplicable := []string{"111222300561", "111222327556"}
		var listing struct{ Jobs []struct{ ID json.Number } }
		Expect(json.Unmarshal(fake.Served("attempt-1/jobs.json"), &listing)).To(Succeed())
		ran := 0
		for _, job := range listing.Jobs {
			id := job.ID.String()
			logURL := fake.URL() + "/repos/rosenhouse/lg/actions/jobs/" + id + "/logs"
			raw, err := os.ReadFile(filepath.Join(jobDir(attempts[0], id), "log.txt.tombstone"))
			Expect(err).NotTo(HaveOccurred())
			Expect(filepath.Join(jobDir(attempts[0], id), "log.txt")).NotTo(BeAnExistingFile())
			var tombstone map[string]any
			Expect(json.Unmarshal(raw, &tombstone)).To(Succeed())
			if slices.Contains(notApplicable, id) {
				Expect(tombstone).To(SatisfyAll(
					HaveKeyWithValue("reason", "not_applicable"),
					HaveKeyWithValue("http_status", BeNil()),
					HaveKeyWithValue("url", logURL),
				), "job %s", id)
				Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", HaveSuffix("/jobs/"+id+"/logs"))))
				continue
			}
			ran++
			Expect(tombstone).To(SatisfyAll(
				HaveKeyWithValue("reason", "deleted"),
				HaveKeyWithValue("http_status", BeEquivalentTo(404)),
				HaveKeyWithValue("message", "Not Found"),
				HaveKeyWithValue("url", logURL),
			), "job %s", id)
		}
		Expect(ran).To(Equal(10))
	})
})

var _ = Describe("lg sync with LG_TEST_NOW", Label("failures"), func() {
	It("starts its clock there, for fetched_at and for log_grace", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(37129738159, "logs-deleted")
		env.WriteConfig(fake.URL())
		env.Setenv("LG_TEST_NOW", "2026-10-03T15:00:00Z")

		Expect(env.Sync()).To(gexec.Exit(1))
		Expect(os.ReadDir(env.Data())).To(BeEmpty())

		env.Setenv("LG_TEST_NOW", "2026-10-03T15:31:00Z")
		Expect(env.Sync()).To(gexec.Exit(0))
		fetchJSON, err := filepath.Glob(filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-10-03/37129738159_*/attempt-1/fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchJSON).To(HaveLen(1))
		raw, err := os.ReadFile(fetchJSON[0])
		Expect(err).NotTo(HaveOccurred())
		var fetch struct {
			FetchedAt time.Time `json:"fetched_at"`
		}
		Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
		Expect(fetch.FetchedAt).To(BeTemporally("~", time.Date(2026, 10, 3, 15, 31, 0, 0, time.UTC), harness.ExitTimeout))
	})
})

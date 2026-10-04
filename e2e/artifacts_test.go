package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("lg sync at after-attempt-1", Label("artifacts"), func() {
	It("publishes the 4 artifacts as artifacts/<id>_<slug>/ with artifact.json, an artifact.zip byte-identical to the recording, and a fetch.json recording run_created_at, run_attempt_at_fetch, run_status_at_fetch, and the run's workflow_id, workflow name, event, PR numbers and display_title", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())

		Expect(env.Sync()).To(gexec.Exit(0))
		artifacts := filepath.Join(env.Data(), fixtureRunDir, "artifacts")
		Expect(filepath.Glob(filepath.Join(artifacts, "*"))).To(ConsistOf(
			filepath.Join(artifacts, "11276401837_flaky-report"),
			filepath.Join(artifacts, "11276272069_pass-artifact"),
			filepath.Join(artifacts, "11276182467_flaky-report-overwrite"),
			filepath.Join(artifacts, "11275917910_expires-in-1-day"),
		))
		var listing struct{ Artifacts []json.RawMessage }
		Expect(json.Unmarshal(fake.Served("artifacts.json"), &listing)).To(Succeed())
		Expect(listing.Artifacts).To(HaveLen(4))
		for _, raw := range listing.Artifacts {
			var artifact struct {
				ID   int64
				Name string
			}
			Expect(json.Unmarshal(raw, &artifact)).To(Succeed())
			dir := filepath.Join(artifacts, fmt.Sprintf("%d_%s", artifact.ID, artifact.Name))

			Expect(os.ReadFile(filepath.Join(dir, "artifact.json"))).To(Equal(indented(raw)))
			recorded, err := os.ReadFile(filepath.Join(recordings.Dir(fixtureRun, "after-attempt-1"), "artifacts", fmt.Sprintf("%d.zip", artifact.ID)))
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(filepath.Join(dir, "artifact.zip"))).To(Equal(recorded))
			Expect(readJSON(filepath.Join(dir, "fetch.json"))).To(SatisfyAll(
				HaveKeyWithValue("run_created_at", "2026-10-03T14:22:54Z"),
				HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(1)),
				HaveKeyWithValue("run_status_at_fetch", "completed"),
				HaveKeyWithValue("workflow_id", BeEquivalentTo(373958224)),
				HaveKeyWithValue("workflow_name", "lg-fixture"),
				HaveKeyWithValue("event", "push"),
				HaveKeyWithValue("pr_numbers", BeEmpty()),
				HaveKeyWithValue("display_title", "Add lg-fixture workflow for recording Actions API shapes"),
				HaveKeyWithValue("sources", SatisfyAll(
					HaveKeyWithValue("artifact.json", HaveKeyWithValue("url", fake.URL()+"/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100")),
					HaveKeyWithValue("artifact.zip", SatisfyAll(
						HaveKeyWithValue("url", fmt.Sprintf("%s/repos/rosenhouse/lg/actions/artifacts/%d/zip", fake.URL(), artifact.ID)),
						HaveKeyWithValue("status", BeEquivalentTo(http.StatusOK)),
						HaveKeyWithValue("bytes", BeEquivalentTo(len(recorded))),
						HaveKeyWithValue("sha256", fmt.Sprintf("%x", sha256.Sum256(recorded))),
					)),
				)),
			), "artifact %d", artifact.ID)
		}
	})
})

var _ = Describe("lg sync at after-attempt-1, then -2, then -3", Label("artifacts"), func() {
	It("keeps all 12 artifact ids, including flaky-report 11276401837 and 11276267449 and the overwritten 11276182467", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())

		Expect(env.Sync()).To(gexec.Exit(0))
		for _, stage := range []string{"after-attempt-2", "after-attempt-3"} {
			Expect(fake.Advance(fixtureRun, stage)).To(Succeed())
			Expect(env.Sync()).To(gexec.Exit(0))
		}
		zips, err := filepath.Glob(filepath.Join(env.Data(), fixtureRunDir, "artifacts", "*", "artifact.zip"))
		Expect(err).NotTo(HaveOccurred())
		Expect(zips).To(HaveLen(12))
		Expect(zips).To(ContainElements(
			HaveSuffix("/11276401837_flaky-report/artifact.zip"),
			HaveSuffix("/11276267449_flaky-report/artifact.zip"),
			HaveSuffix("/11276182467_flaky-report-overwrite/artifact.zip"),
		))
	})
})

var _ = Describe("lg sync with artifact_max_bytes in config.yaml", Label("artifacts"), func() {
	It("tombstones a larger artifact as too_large without requesting its zip", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL(), "artifact_max_bytes: 700")

		Expect(env.Sync()).To(gexec.Exit(0))
		artifacts := filepath.Join(env.Data(), fixtureRunDir, "artifacts")
		Expect(readJSON(filepath.Join(artifacts, "11276272069_pass-artifact", "artifact.zip.tombstone"))).To(HaveKeyWithValue("reason", "too_large"))
		Expect(filepath.Join(artifacts, "11276272069_pass-artifact", "artifact.zip")).NotTo(BeAnExistingFile())
		Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", HaveSuffix("/artifacts/11276272069/zip"))))
		Expect(filepath.Join(artifacts, "11276401837_flaky-report", "artifact.zip")).To(BeARegularFile())
	})
})

func readJSON(path string) map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	var fields map[string]any
	Expect(json.Unmarshal(raw, &fields)).To(Succeed())
	return fields
}

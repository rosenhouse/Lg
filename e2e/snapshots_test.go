package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

const flakyReportDir = fixtureRunDir + "/artifacts/11276401837_flaky-report"

// servedArtifacts is the run's artifact listing as served, joined into one array.
func servedArtifacts(fake *fakegithub.Server) []byte {
	GinkgoHelper()
	var listing struct{ Artifacts []json.RawMessage }
	Expect(json.Unmarshal(fake.Served("artifacts.json"), &listing)).To(Succeed())
	elements := make([][]byte, len(listing.Artifacts))
	for i, a := range listing.Artifacts {
		elements[i] = a
	}
	return append(append([]byte("["), bytes.Join(elements, []byte(","))...), ']')
}

// servedArtifact is the element of the run's artifact listing as served.
func servedArtifact(fake *fakegithub.Server, id int64) []byte {
	GinkgoHelper()
	var listing struct{ Artifacts []json.RawMessage }
	Expect(json.Unmarshal(fake.Served("artifacts.json"), &listing)).To(Succeed())
	for _, raw := range listing.Artifacts {
		var a struct{ ID int64 }
		Expect(json.Unmarshal(raw, &a)).To(Succeed())
		if a.ID == id {
			return raw
		}
	}
	Fail("artifact " + strconv.FormatInt(id, 10) + " is not listed")
	return nil
}

// listedInAttempt1 matches an artifact's fetch.json whose artifact.json came
// from the listing taken while the run was at attempt 1.
func listedInAttempt1(fake *fakegithub.Server) types.GomegaMatcher {
	return SatisfyAll(
		HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(1)),
		HaveKeyWithValue("run_status_at_fetch", "completed"),
		HaveKeyWithValue("sources", HaveKeyWithValue("artifact.json", SatisfyAll(
			HaveKeyWithValue("url", fake.URL()+"/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100"),
			HaveKeyWithValue("pages", BeEquivalentTo(1)),
		))),
	)
}

var _ = Describe("attempt-N", Label("artifacts"), func() {
	It("holds artifacts.json as listed in the cycle that published it, with run_attempt_at_fetch in fetch.json", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())

		listed := map[int][]byte{}
		for n, stage := range []string{"after-attempt-1", "after-attempt-2", "after-attempt-3"} {
			Expect(fake.Advance(fixtureRun, stage)).To(Succeed())
			listed[n+1] = servedArtifacts(fake)
			Expect(env.Sync()).To(gexec.Exit(0))
		}
		for n, listing := range listed {
			attempt := filepath.Join(env.Data(), fixtureRunDir, "attempt-"+strconv.Itoa(n))
			Expect(os.ReadFile(filepath.Join(attempt, "artifacts.json"))).To(Equal(indented(listing)), "attempt %d", n)
			Expect(readJSON(filepath.Join(attempt, "fetch.json"))).To(HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(n)))
		}
	})
})

var _ = Describe("an artifact whose zip and whose attempt's log both failed transiently, after a re-run-all deleted it", Label("artifacts"), func() {
	It("gets artifact.json and a deleted tombstone on the next sync", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		listed := servedArtifact(fake, 11276401837)
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		fake.Fail("api", "artifacts/11276401837/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(env.Sync()).To(gexec.Exit(1))
		Expect(os.ReadFile(filepath.Join(env.State(), "pending-artifacts.json"))).To(ContainSubstring(`"id": 11276401837`))
		Expect(fake.Advance(fixtureRun, "after-attempt-3")).To(Succeed())

		Expect(env.Sync()).To(gexec.Exit(0))
		dir := filepath.Join(env.Data(), flakyReportDir)
		Expect(os.ReadFile(filepath.Join(dir, "artifact.json"))).To(Equal(indented(listed)))
		Expect(readJSON(filepath.Join(dir, "artifact.zip.tombstone"))).To(SatisfyAll(
			HaveKeyWithValue("reason", "deleted"),
			HaveKeyWithValue("http_status", BeEquivalentTo(http.StatusNotFound)),
		))
		Expect(readJSON(filepath.Join(dir, "fetch.json"))).To(listedInAttempt1(fake))
	})
})

var _ = Describe("an artifact listed only in an on-disk attempt snapshot, after state/ is deleted", Label("artifacts"), func() {
	It("is retried and tombstoned as deleted when it 404s", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		fake.Fail("api", "artifacts/11276401837/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(env.Sync()).To(gexec.Exit(1))
		Expect(os.RemoveAll(env.State())).To(Succeed())
		Expect(fake.Advance(fixtureRun, "after-attempt-3")).To(Succeed())

		Expect(env.Sync()).To(gexec.Exit(0))
		dir := filepath.Join(env.Data(), flakyReportDir)
		Expect(filepath.Join(dir, "artifact.json")).To(BeARegularFile())
		Expect(readJSON(filepath.Join(dir, "artifact.zip.tombstone"))).To(SatisfyAll(
			HaveKeyWithValue("reason", "deleted"),
			HaveKeyWithValue("http_status", BeEquivalentTo(http.StatusNotFound)),
		))
		Expect(readJSON(filepath.Join(dir, "fetch.json"))).To(listedInAttempt1(fake))
	})
})

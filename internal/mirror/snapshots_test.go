package mirror_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

func readJSONFile(path string) map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	var fields map[string]any
	Expect(json.Unmarshal(raw, &fields)).To(Succeed())
	return fields
}

var _ = Describe("an attempt's fetch.json", Label("artifacts"), func() {
	It("records the artifact listing that artifacts.json holds", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readJSONFile(filepath.Join(attemptDir(env, runID, 1), "fetch.json"))).To(HaveKeyWithValue("sources",
			HaveKeyWithValue("artifacts.json", SatisfyAll(
				HaveKeyWithValue("url", env.Fake.URL()+"/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100"),
				HaveKeyWithValue("pages", BeEquivalentTo(1)),
			)),
		))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when a run's artifact listing fails transiently", Label("artifacts"), func() {
	It("publishes no attempt of that run, since it would lack its snapshot, and the next cycle publishes it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "runs/37129390741/artifacts", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(filepath.Join(attemptDir(env, runID, 1), "artifacts.json")).To(BeARegularFile())
	}, cycleTimeout)
})

func listedArtifact(id int64) github.Artifact {
	raw := fmt.Sprintf(`{"id":%d,"name":"a%d"}`, id, id)
	return github.Artifact{Artifact: model.Artifact{ID: id, Name: fmt.Sprintf("a%d", id)}, Raw: json.RawMessage(raw)}
}

func writeSnapshot(attemptDir string, artifacts ...github.Artifact) {
	GinkgoHelper()
	raws := make([]json.RawMessage, len(artifacts))
	for i, a := range artifacts {
		raws[i] = a.Raw
	}
	raw, err := json.Marshal(raws)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.MkdirAll(attemptDir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(attemptDir, "artifacts.json"), raw, 0o644)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(attemptDir, "fetch.json"), []byte(`{"sources":{}}`), 0o644)).To(Succeed())
}

func ids(artifacts []github.Artifact) []int64 {
	var ids []int64
	for _, a := range artifacts {
		ids = append(ids, a.ID)
	}
	return ids
}

var _ = Describe("the retry set", Label("artifacts"), func() {
	It("is the ids in every attempt-*/artifacts.json, in state/pending-artifacts.json and in the current listing, minus the artifact dirs on disk", func() {
		env := harness.InProcess()
		runDir := filepath.Join(env.Data(), "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "1_w_b")
		writeSnapshot(layout.AttemptDir(runDir, 1), listedArtifact(1), listedArtifact(7))
		writeSnapshot(layout.AttemptDir(runDir, 2), listedArtifact(2), listedArtifact(3))
		for _, id := range []int64{1, 6} {
			Expect(os.MkdirAll(layout.ArtifactDir(runDir, id, fmt.Sprintf("a%d", id)), 0o755)).To(Succeed())
		}
		listing := []github.Artifact{listedArtifact(5), listedArtifact(6), listedArtifact(3)}
		pending := []github.Artifact{listedArtifact(4), listedArtifact(5)}

		retry, err := env.Mirror.RetrySet(runDir, listing, pending)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(retry)).To(Equal([]int64{5, 3, 4, 7, 2}))
		Expect(retry[3].Raw).To(MatchJSON(listedArtifact(7).Raw))
	})

	It("takes nothing from an attempt dir with no artifacts.json, as lg wrote before snapshots", func() {
		env := harness.InProcess()
		runDir := filepath.Join(env.Data(), "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "1_w_b")
		Expect(os.MkdirAll(layout.AttemptDir(runDir, 1), 0o755)).To(Succeed())

		retry, err := env.Mirror.RetrySet(runDir, []github.Artifact{listedArtifact(5)}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(retry)).To(Equal([]int64{5}))
	})
})

// readPending reads state/pending-artifacts.json as run id -> artifact objects.
func readPending(env *harness.InProcessEnv) map[string][]json.RawMessage {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(env.State(), "pending-artifacts.json"))
	Expect(err).NotTo(HaveOccurred())
	var pending map[string]struct{ Artifacts []struct{ Artifact json.RawMessage } }
	Expect(json.Unmarshal(raw, &pending)).To(Succeed())
	artifacts := map[string][]json.RawMessage{}
	for runID, run := range pending {
		for _, c := range run.Artifacts {
			artifacts[runID] = append(artifacts[runID], c.Artifact)
		}
	}
	return artifacts
}

// servedArtifactsByID is the run's artifact listing as served, by id.
func servedArtifactsByID(env *harness.InProcessEnv) map[string]json.RawMessage {
	GinkgoHelper()
	var listing struct{ Artifacts []json.RawMessage }
	Expect(json.Unmarshal(env.Fake.Served("artifacts.json"), &listing)).To(Succeed())
	byID := map[string]json.RawMessage{}
	for _, raw := range listing.Artifacts {
		var a struct{ ID json.Number }
		Expect(json.Unmarshal(raw, &a)).To(Succeed())
		byID[a.ID.String()] = raw
	}
	return byID
}

var _ = Describe("state/pending-artifacts.json", Label("artifacts"), func() {
	It("keeps each listed artifact object until its dir exists", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(readPending(env)).To(HaveKeyWithValue("37129390741", ConsistOf(MatchJSON(servedArtifactsByID(env)[flakyReport]))))

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readPending(env)).To(BeEmpty())
	}, cycleTimeout)

	It("holds the run's listed artifacts before any zip is requested", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}})

		Expect(env.Sync(ctx)).To(BeBlocked(failure.RateLimit))
		var listed []any
		for _, raw := range servedArtifactsByID(env) {
			listed = append(listed, MatchJSON(raw))
		}
		Expect(readPending(env)).To(HaveKeyWithValue("37129390741", ConsistOf(listed...)))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when an attempt's artifacts.json is not JSON", Label("artifacts"), func() {
	It("fails only that run and publishes the others", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(os.Chmod(attemptDir(env, runID, 1), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(attemptDir(env, runID, 1), "artifacts.json"), []byte("<"), 0o644)).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())

		err := env.Sync(ctx)
		Expect(err).To(MatchError(ContainSubstring("artifacts.json")))
		Expect(mirror.RunScoped(err)).To(BeTrue())
		Expect(env.AttemptDirs(deletedRun)).To(HaveLen(1))
	}, cycleTimeout)
})

var _ = Describe("an artifact published from state/pending-artifacts.json", Label("artifacts"), func() {
	It("keeps &, < and > in artifact.json as listed, so rg finds them", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		listing := strings.Replace(string(env.Fake.Served("artifacts.json")), `"head_branch":"lg-fixture"`, `"head_branch":"fix&<feat>"`, -1)
		env.Fake.Fail("api", "runs/37129390741/artifacts", fakegithub.Fault{Status: http.StatusOK, Body: listing, Times: 1})
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.Fake.Advance(runID, "after-attempt-3")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		raw, err := os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"head_branch": "fix&<feat>"`))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when state/pending-artifacts.json does not parse", Label("artifacts"), func() {
	DescribeTable("moves it aside, reports it, and syncs as if it were empty",
		func(ctx SpecContext, content string) {
			env := harness.InProcess()
			Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
			pending := filepath.Join(env.State(), "pending-artifacts.json")
			Expect(os.WriteFile(pending, []byte(content), 0o644)).To(Succeed())

			err := env.Sync(ctx)
			Expect(err).To(MatchError(ContainSubstring(pending)))
			Expect(mirror.RunScoped(err)).To(BeTrue())
			Expect(env.AttemptDirs(runID)).To(HaveLen(1))
			Expect(os.ReadFile(pending + ".corrupt")).To(Equal([]byte(content)))

			Expect(env.Sync(ctx)).To(Succeed())
		},
		Entry("empty", "", cycleTimeout),
		Entry("an array", "[]", cycleTimeout),
		Entry("a run id that is not a number", `{"abc":[]}`, cycleTimeout),
		Entry("a run that is an array", `{"1":[]}`, cycleTimeout),
		Entry("a run whose id is not its key", `{"1":{"run":{"id":2},"artifacts":[]}}`, cycleTimeout),
		Entry("an artifact id that is not a number", `{"1":{"run":{"id":1},"artifacts":[{"artifact":{"id":"x"}}]}}`, cycleTimeout),
		Entry("an entry with no artifact", `{"1":{"run":{"id":1},"artifacts":[{}]}}`, cycleTimeout),
		Entry("an entry with a null artifact", `{"1":{"run":{"id":1},"artifacts":[{"artifact":null}]}}`, cycleTimeout),
	)

	It("syncs a null file as an empty one", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(os.WriteFile(filepath.Join(env.State(), "pending-artifacts.json"), []byte("null"), 0o644)).To(Succeed())

		Expect(env.Sync(ctx)).To(BeTransient())
	}, cycleTimeout)
})

var _ = Describe("a snapshot whose listing a re-run overtook", Label("artifacts"), func() {
	It("records in fetch.json the run_attempt read after the listing, not the one ListRuns gave", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		heldZip := "artifacts/11276128157/zip"
		release := env.Fake.Hold(heldZip)
		cycled := make(chan error, 1)
		go func() { cycled <- env.Mirror.Cycle(ctx) }()
		Eventually(env.Fake.Requests).WithTimeout(10 * time.Second).Should(ContainElement(HaveField("Path", HaveSuffix(heldZip))))
		Expect(env.Fake.Advance(runID, "after-attempt-3")).To(Succeed())
		release()
		Eventually(cycled).WithTimeout(10 * time.Second).Should(Receive())

		attempt1 := attemptDir(env, runID, 1)
		Expect(os.ReadFile(filepath.Join(attempt1, "artifacts.json"))).To(MatchJSON(jsonField(env.Fake.Served("artifacts.json"), "artifacts")))
		Expect(readJSONFile(filepath.Join(attempt1, "fetch.json"))).To(HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(3)))
		Expect(readJSONFile(filepath.Join(artifactDir(env, runID, "11275729552"), "fetch.json"))).To(SatisfyAll(
			HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(3)),
			HaveKeyWithValue("run_status_at_fetch", "completed"),
		))
	}, cycleTimeout)
})

// jsonField is a field of a JSON object.
func jsonField(raw []byte, name string) json.RawMessage {
	GinkgoHelper()
	var object map[string]json.RawMessage
	Expect(json.Unmarshal(raw, &object)).To(Succeed())
	return object[name]
}

var _ = Describe("an artifact pending for a run that was then deleted", Label("artifacts"), func() {
	It("is retried, tombstoned as deleted and dropped from pending, though no listing names the run", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		env.Fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(env.Sync(ctx)).To(BeTransient())
		env.Fake.Remove(runID)

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, flakyReport)).To(HaveKeyWithValue("reason", "deleted"))
		Expect(readJSONFile(filepath.Join(artifactDir(env, runID, flakyReport), "fetch.json"))).To(HaveKeyWithValue("run_attempt_at_fetch", BeEquivalentTo(1)))
		Expect(readPending(env)).To(BeEmpty())
	}, cycleTimeout)
})

var _ = Describe("an artifact pending for a run of another repo", Label("artifacts"), func() {
	It("stays pending and untouched while lg syncs this repo", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		other := `{"5":{"run":{"id":5,"repository":{"full_name":"other/repo"}},"artifacts":[{"artifact":{"id":9,"name":"a"}}]}}`
		Expect(os.WriteFile(filepath.Join(env.State(), "pending-artifacts.json"), []byte(other), 0o644)).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readPending(env)).To(HaveKeyWithValue("5", ConsistOf(MatchJSON(`{"id":9,"name":"a"}`))))
		Expect(requestedZip(env, "9")).To(BeFalse())
	}, cycleTimeout)
})

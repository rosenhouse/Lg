package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// decoded gives each line lg printed as a JSON object.
func decoded(session *gexec.Session) []map[string]any {
	GinkgoHelper()
	var objects []map[string]any
	for _, line := range outputLines(session) {
		var object map[string]any
		Expect(json.Unmarshal([]byte(line), &object)).To(Succeed(), line)
		objects = append(objects, object)
	}
	return objects
}

// where runs lg where with args to exit 0 and decodes what it printed.
func where(env *harness.Env, args ...string) []map[string]any {
	GinkgoHelper()
	session := env.Lg(append([]string{"where"}, args...)...)
	Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
	return decoded(session)
}

// These specs only read the store, so they share one sync. Its LG_HOME holds
// a colon, as a hit's path may.
var _ = Describe("lg where", Ordered, ContinueOnFailure, Label("where"), func() {
	var (
		env         *harness.Env
		a           = scenario.Archaeology()
		running     = scenario.WithPullRequests(scenario.WithEvent(scenario.InProgress(scenario.CloneAt(9, "after-attempt-1", harness.DefaultNow().Add(-time.Hour)), 1), "pull_request"), 43)
		logsDeleted = scenario.Recorded(logsDeletedRun, "logs-deleted")
	)

	BeforeAll(func() {
		env = harness.New(lgPath)
		env.Setenv("LG_HOME", filepath.Join(GinkgoT().TempDir(), "lg:home"))
		syncArchaeology(env, running, logsDeleted)
	})

	It("decodes 'path:line:text' into JSON with host, repo, run_id, attempt, job_id, job, workflow, branch, sha, event, prs, created_at, job_conclusion, line and html_url", func() {
		log := glob(runDirOf(env, a.PR42.ID), "attempt-1", "jobs", "*_flaky", "log.txt")[0]
		grep := env.Sh("grep -Hn 'LG_MARKER flaky failure attempt=1' '" + log + "'")
		Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
		hit := outputLines(grep)[0]

		session := env.Lg("where", hit)
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(outputLines(session)).To(HaveExactElements(MatchJSON(`{
			"path": ` + quote(log) + `,
			"host": "github.com",
			"repo": "rosenhouse/Lg",
			"run_id": 7,
			"attempt": 1,
			"job_id": 7111221289888,
			"job": "flaky",
			"carried_forward": false,
			"workflow": "lg-fixture",
			"branch": "fix-flake",
			"sha": "7777777777777777777777777777777777777777",
			"event": "pull_request",
			"prs": [42],
			"created_at": "2026-09-28T12:00:00Z",
			"job_conclusion": "failure",
			"line": 107,
			"text": "2026-10-03T14:23:01.2367849Z LG_MARKER flaky failure attempt=1",
			"html_url": "https://github.com/rosenhouse/Lg/actions/runs/7/job/7111221289888"
		}`)))
	})

	It("decodes a carried-forward job's job.json with carried_forward true, original_job_id and the original log path", func() {
		run := runDirOf(env, a.Rerun.ID)
		job := glob(run, "attempt-2", "jobs", "8111221662305_*", "job.json")[0]

		Expect(where(env, job)).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("path", job),
			HaveKeyWithValue("attempt", BeEquivalentTo(2)),
			HaveKeyWithValue("job_id", BeEquivalentTo(8111221662305)),
			HaveKeyWithValue("job", "build (ubuntu-latest, 1.23)"),
			HaveKeyWithValue("carried_forward", true),
			HaveKeyWithValue("original_job_id", BeEquivalentTo(8111221289911)),
			HaveKeyWithValue("original_log", filepath.Join(run, "attempt-1", "jobs", "8111221289911_build-ubuntu-latest-1.23", "log.txt")),
			Not(HaveKey("line")),
		)))
	})

	It("accepts absolute paths, data-root-relative paths and hits on stdin, printing one JSON object per input", func() {
		log := passLog(env, a.MainSeptember.ID)
		rel, err := filepath.Rel(env.Data(), log)
		Expect(err).NotTo(HaveOccurred())
		mainSeptember := SatisfyAll(
			HaveKeyWithValue("path", log),
			HaveKeyWithValue("run_id", BeEquivalentTo(a.MainSeptember.ID)),
			HaveKeyWithValue("job", "pass"),
			HaveKeyWithValue("prs", Equal([]any{})),
			Not(HaveKey("line")),
		)
		Expect(where(env, log, rel)).To(HaveExactElements(mainSeptember, mainSeptember))

		hits := env.Sh("lg paths --branch main --branch release-3 --since 30d -0 | xargs -0 grep -Hn 'foo bar' | lg where")
		Eventually(hits, harness.ExitTimeout).Should(gexec.Exit(0))
		hitOf := func(id int64) OmegaMatcher {
			return SatisfyAll(
				HaveKeyWithValue("path", passLog(env, id)),
				HaveKeyWithValue("run_id", BeEquivalentTo(id)),
				HaveKeyWithValue("line", BeEquivalentTo(1)),
				HaveKeyWithValue("text", ContainSubstring(scenario.FooBar)),
			)
		}
		Expect(decoded(hits)).To(ConsistOf(hitOf(a.MainSeptember.ID), hitOf(a.Release3.ID)))
	})

	It("resolves a relative path against the working directory when data/ has no such path, as in W4", func() {
		repo := filepath.Join(env.Data(), "github.com", "rosenhouse", "Lg")
		hits := env.Sh("cd '" + repo + "' && grep -rn 'foo bar' runs/2026-09-10 | lg where")
		Eventually(hits, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(decoded(hits)).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("path", passLog(env, a.MainSeptember.ID)),
			HaveKeyWithValue("line", BeEquivalentTo(1)),
		)))
	})

	It("decodes an artifact of a run whose attempt is not on disk from artifact.json and fetch.json", func() {
		Expect(filepath.Glob(filepath.Join(runDirOf(env, 9), "attempt-*"))).To(BeEmpty())
		zip := glob(runDirOf(env, 9), "artifacts", "9011276401837_*", "artifact.zip")[0]

		Expect(where(env, zip)).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("run_id", BeEquivalentTo(9)),
			HaveKeyWithValue("artifact_id", BeEquivalentTo(9011276401837)),
			HaveKeyWithValue("artifact", "flaky-report"),
			HaveKeyWithValue("workflow", "lg-fixture"),
			HaveKeyWithValue("branch", "lg-fixture"),
			HaveKeyWithValue("sha", "1a51097dadb5b55978ac401b93f1ca9d8d317b02"),
			HaveKeyWithValue("event", "pull_request"),
			HaveKeyWithValue("prs", ConsistOf(BeEquivalentTo(43))),
			HaveKeyWithValue("created_at", "2026-10-03T17:00:00Z"),
			HaveKeyWithValue("attributed_attempt", BeNil()),
			HaveKeyWithValue("attribution", "unknown"),
			Not(HaveKey("attempt")),
		)))
	})

	It("decodes a tombstone into reason, http_status and message", func() {
		tombstone := glob(runDirOf(env, logsDeletedRun), "attempt-1", "jobs", "111222299886_flaky", "log.txt.tombstone")[0]

		Expect(where(env, tombstone)).To(HaveExactElements(SatisfyAll(
			HaveKeyWithValue("job", "flaky"),
			HaveKeyWithValue("job_conclusion", "failure"),
			HaveKeyWithValue("reason", "deleted"),
			HaveKeyWithValue("http_status", BeEquivalentTo(404)),
			HaveKeyWithValue("message", "Not Found"),
		)))
	})

	It("exits 1 naming the path when it is outside the store", func() {
		elsewhere := filepath.Join(GinkgoT().TempDir(), "log.txt")
		Expect(os.WriteFile(elsewhere, []byte("foo bar\n"), 0o644)).To(Succeed())

		session := env.Lg("where", elsewhere+":1:foo bar")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(session.Out.Contents()).To(BeEmpty())
		Expect(string(session.Err.Contents())).To(ContainSubstring(elsewhere + " is outside the store"))
	})

	It("works with state/lg.db deleted", func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			Expect(os.RemoveAll(filepath.Join(env.State(), "lg.db"+suffix))).To(Succeed())
		}
		run := runDirOf(env, a.Rerun.ID)
		job := glob(run, "attempt-2", "jobs", "8111221662305_*", "job.json")[0]
		zip := glob(run, "artifacts", "8011276052917_*", "artifact.zip")[0]

		Expect(where(env, job, zip)).To(HaveExactElements(
			HaveKeyWithValue("original_job_id", BeEquivalentTo(8111221289911)),
			SatisfyAll(HaveKeyWithValue("attributed_attempt", BeEquivalentTo(2)), HaveKeyWithValue("attribution", "timestamp")),
		))
		Expect(filepath.Join(env.State(), "lg.db")).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg where", Label("where"), func() {
	It("decodes an artifact.zip path into artifact id, name and attributed_attempt with its attribution method", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
		Expect(env.Sync()).To(gexec.Exit(0))
		artifacts := filepath.Join(env.Data(), fixtureRunDir, "artifacts")

		Expect(where(env,
			filepath.Join(artifacts, "11276052917_rerun-only-attempt-2", "artifact.zip"),
			filepath.Join(artifacts, "11276401837_flaky-report", "artifact.zip"),
		)).To(HaveExactElements(
			SatisfyAll(
				HaveKeyWithValue("artifact_id", BeEquivalentTo(11276052917)),
				HaveKeyWithValue("artifact", "rerun-only-attempt-2"),
				HaveKeyWithValue("attributed_attempt", BeEquivalentTo(2)),
				HaveKeyWithValue("attribution", "listing-diff"),
				HaveKeyWithValue("html_url", "https://github.com/rosenhouse/Lg/actions/runs/37129390741"),
			),
			SatisfyAll(
				HaveKeyWithValue("artifact_id", BeEquivalentTo(11276401837)),
				HaveKeyWithValue("artifact", "flaky-report"),
				HaveKeyWithValue("attributed_attempt", BeEquivalentTo(1)),
				HaveKeyWithValue("attribution", "timestamp"),
			),
		))
	})
})

// quote gives s as a JSON string.
func quote(s string) string {
	b, err := json.Marshal(s)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

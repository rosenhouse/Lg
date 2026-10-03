package e2e_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

const fixtureRunDir = "github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture"

var _ = Describe("lg sync when log 111221289888 returns 500", Label("store"), func() {
	var (
		env      *harness.Env
		attempt1 string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		env.WriteConfig(fake.URL())
		attempt1 = filepath.Join(env.Data(), fixtureRunDir, "attempt-1")
	})

	It("exits 1, publishes no attempt-1, and BeAppendOnlyFrom holds", func() {
		Expect(env.Sync()).To(gexec.Exit(1))
		Expect(attempt1).NotTo(BeAnExistingFile())
	})

	It("publishes the complete attempt-1 on the next sync after the fault clears", func() {
		Expect(env.Sync()).To(gexec.Exit(1))
		Expect(env.Sync()).To(gexec.Exit(0))

		files := map[string]int{}
		for path, entry := range treesnap.Snapshot(attempt1) {
			if entry.Mode.IsRegular() {
				files[filepath.Base(path)]++
			}
		}
		Expect(files).To(Equal(map[string]int{
			"attempt.json": 1, "jobs.json": 1, "job.json": 12, "log.txt": 10, "log.txt.tombstone": 2,
		}))
		recorded, err := os.ReadFile(filepath.Join(fakegithub.Recording(fixtureRun, "after-attempt-1"), "attempt-1/logs/111221289888.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(jobDir(attempt1, "111221289888") + "/log.txt")).To(Equal(recorded))
	})
})

var _ = Describe("a second lg sync with nothing new", Label("store"), func() {
	It("changes no file under data/ and requests no attempt, job or log", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		before := treesnap.Snapshot(env.Data())
		requested := len(fake.Requests())

		Expect(env.Sync()).To(gexec.Exit(0))

		Expect(treesnap.Snapshot(env.Data())).To(Equal(before))
		Expect(fake.Requests()[requested:]).NotTo(ContainElement(HaveField("Path",
			Or(ContainSubstring("/attempts/"), ContainSubstring("/jobs/"), ContainSubstring("/logs")))))
	})
})

var _ = Describe("lg sync on a fresh LG_HOME", Label("store"), func() {
	It("writes FORMAT `lg-store 1` and .rgignore listing state/ and tmp/", func() {
		env := harness.New(lgPath)
		env.Setenv("LG_HOME", filepath.Join(GinkgoT().TempDir(), "lg"))
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())

		Expect(env.Sync()).To(gexec.Exit(0))

		Expect(os.ReadFile(filepath.Join(env.Store(), "FORMAT"))).To(Equal([]byte("lg-store 1\n")))
		Expect(os.ReadFile(filepath.Join(env.Store(), ".rgignore"))).To(Equal([]byte("state/\ntmp/\n")))
		for _, dir := range []string{env.Store(), env.State(), env.Tmp()} {
			Expect(os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("rgignore probe"), 0o644)).To(Succeed())
		}
		rg := env.Sh("cd '" + env.Store() + "' && rg -l 'rgignore probe'")
		Eventually(rg, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(rg.Out.Contents())).To(Equal("probe.txt\n"))
	})
})

var _ = Describe("lg with FORMAT `lg-store 2`", Label("store"), func() {
	It("refuses to run, naming the version", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(os.MkdirAll(env.Store(), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(env.Store(), "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		for _, command := range []string{"sync", "paths"} {
			session := env.Lg(command)
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1), command)
			Expect(string(session.Err.Contents())).To(ContainSubstring(`"lg-store 2"`), command)
		}
		Expect(env.Data()).NotTo(BeAnExistingFile())
		Expect(fake.Requests()).To(BeEmpty())
	})
})

var _ = Describe("lg sync", Label("store"), func() {
	It("removes leftover tmp/ entries while holding state/write.lock", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		leftover := filepath.Join(env.Tmp(), "unit-leftover", "log.txt")
		Expect(os.MkdirAll(filepath.Dir(leftover), 0o755)).To(Succeed())
		Expect(os.WriteFile(leftover, []byte("partial"), 0o644)).To(Succeed())
		held, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{})
		Expect(err).NotTo(HaveOccurred())

		session := env.Lg("sync")
		Consistently(func() string { return leftover }, time.Second).Should(BeAnExistingFile())
		Expect(session.ExitCode()).To(Equal(-1), "sync ran while the lock was held")

		Expect(held.Release()).To(Succeed())
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(os.ReadDir(env.Tmp())).To(BeEmpty())
	})
})

var _ = Describe("two concurrent lg sync processes", Label("store"), func() {
	It("serialize on state/write.lock: the second waits while fakegithub holds the first's log request, both exit 0, and attempt-1 is published once", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		release := fake.Hold("jobs/111221289888/logs")

		first := env.Lg("sync")
		Eventually(fake.Requests, harness.ExitTimeout).Should(ContainElement(HaveField("Path", HaveSuffix("jobs/111221289888/logs"))))
		requested := len(fake.Requests())
		second := env.Lg("sync")
		Consistently(func() int { return len(fake.Requests()) }, time.Second).Should(Equal(requested))
		Expect(second.ExitCode()).To(Equal(-1), "second sync exited while the first held the lock")

		release()
		Eventually(first, harness.ExitTimeout).Should(gexec.Exit(0))
		Eventually(second, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(env.Data(), fixtureRunDir, "attempt-1")).To(BeADirectory())
		Expect(requestsTo(fake, "/attempts/1/jobs")).To(Equal(1))
		Expect(requestsTo(fake, "/logs")).To(Equal(10))
	})
})

// requestsTo counts the API requests whose path ends in suffix.
func requestsTo(fake *fakegithub.Server, suffix string) int {
	n := 0
	for _, r := range fake.Requests() {
		if r.Host == "api" && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

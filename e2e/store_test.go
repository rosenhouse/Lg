package e2e_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

const fixtureRunDir = "github.com/rosenhouse/Lg/runs/2026-10-03/37129390741_lg-fixture_lg-fixture"

var _ = Describe("lg sync when log 111221289888 returns 500", Label("store"), func() {
	const failingLog = "jobs/111221289888/logs"
	var (
		env      *harness.Env
		fake     *fakegithub.Server
		attempt1 string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.Start(fixtureRun, "after-attempt-1")
		fake.Fail("api", failingLog, fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		env.WriteConfig(fake.URL())
		attempt1 = filepath.Join(env.Data(), fixtureRunDir, "attempt-1")
	})

	// syncThroughFault runs lg sync, checking that no file of attempt-1 is
	// in data/ while the failing log request is held, part way through staging it.
	syncThroughFault := func() *gexec.Session {
		GinkgoHelper()
		release := fake.Hold(failingLog)
		_, wait := env.StartSync()
		Eventually(fake.Requests, harness.ExitTimeout).Should(ContainElement(HaveField("Path", HaveSuffix(failingLog))))
		Expect(treesnap.Snapshot(env.Data())).NotTo(HaveKey(HavePrefix(fixtureRunDir + "/attempt-1")))
		release()
		return wait()
	}

	It("exits 1, publishes no attempt-1, and BeAppendOnlyFrom holds", func() {
		Expect(store.Init(env.Store())).To(Succeed())
		earlier := filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/2026-10-02/1_x_main/attempt-1/log.txt")
		Expect(os.MkdirAll(filepath.Dir(earlier), 0o755)).To(Succeed())
		Expect(os.WriteFile(earlier, []byte("earlier"), 0o644)).To(Succeed())

		Expect(syncThroughFault()).To(gexec.Exit(1))
		Expect(attempt1).NotTo(BeAnExistingFile())
	})

	It("publishes the complete attempt-1 on the next sync after the fault clears", func() {
		Expect(syncThroughFault()).To(gexec.Exit(1))
		Expect(env.Sync()).To(gexec.Exit(0))

		files := map[string]int{}
		for path, entry := range treesnap.Snapshot(attempt1) {
			if entry.Mode.IsRegular() {
				files[filepath.Base(path)]++
			}
		}
		Expect(files).To(Equal(map[string]int{
			"attempt.json": 1, "jobs.json": 1, "artifacts.json": 1, "fetch.json": 1, "job.json": 12, "log.txt": 10, "log.txt.tombstone": 2,
		}))
		recorded, err := os.ReadFile(filepath.Join(recordings.Dir(fixtureRun, "after-attempt-1"), "attempt-1/logs/111221289888.txt"))
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
		Expect(os.ReadFile(filepath.Join(env.Store(), ".rgignore"))).To(Equal([]byte("/state/\n/tmp/\n")))
		shown := []string{"probe.txt", "data/github.com/acme/tmp/probe.txt", "data/github.com/state/x/probe.txt"}
		for _, path := range append([]string{"state/probe.txt", "tmp/probe.txt"}, shown...) {
			path = filepath.Join(env.Store(), path)
			Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
			Expect(os.WriteFile(path, []byte("rgignore probe"), 0o644)).To(Succeed())
		}

		fromStore := env.Sh("cd '" + env.Store() + "' && rg -l 'rgignore probe'")
		Eventually(fromStore, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(strings.Fields(string(fromStore.Out.Contents()))).To(ConsistOf(shown))
		fromRoot := env.Sh("cd / && rg -l 'rgignore probe' \"$(lg root)\"")
		Eventually(fromRoot, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(strings.Fields(string(fromRoot.Out.Contents()))).To(ConsistOf(
			filepath.Join(env.Store(), shown[1]), filepath.Join(env.Store(), shown[2])))
	})
})

var _ = Describe("lg with FORMAT `lg-store 2`", Label("store"), func() {
	It("refuses to run, naming the version", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(os.MkdirAll(env.Store(), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(env.Store(), "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		for _, command := range []string{"sync", "paths", "root"} {
			session := env.Lg(command)
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1), command)
			Expect(string(session.Err.Contents())).To(ContainSubstring(`"lg-store 2"`), command)
		}
		Expect(os.ReadDir(env.Store())).To(HaveExactElements(HaveField("Name()", "FORMAT")))
		Expect(fake.Requests()).To(BeEmpty())
	})
})

var _ = Describe("lg with an LG_HOME holding files lg did not write", Label("store"), func() {
	It("refuses to run and leaves LG_HOME untouched", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		notes := filepath.Join(env.Tmp(), "project", "notes.txt")
		Expect(os.MkdirAll(filepath.Dir(notes), 0o755)).To(Succeed())
		Expect(os.WriteFile(notes, []byte("precious"), 0o644)).To(Succeed())
		before := treesnap.Snapshot(env.Store())

		for _, command := range []string{"sync", "paths", "root"} {
			session := env.Lg(command)
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1), command)
			Expect(string(session.Err.Contents())).To(ContainSubstring("point LG_HOME at an empty or new dir"), command)
		}
		Expect(treesnap.Snapshot(env.Store())).To(Equal(before))
		Expect(fake.Requests()).To(BeEmpty())
	})
})

var _ = Describe("lg sync", Label("store"), func() {
	It("removes leftover tmp/ entries while holding state/write.lock", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		leftovers := []string{filepath.Join(env.Tmp(), "unit-leftover", "log.txt"), filepath.Join(env.Tmp(), "trash", "x", "log.txt")}
		for _, leftover := range leftovers {
			Expect(os.MkdirAll(filepath.Dir(leftover), 0o755)).To(Succeed())
			Expect(os.WriteFile(leftover, []byte("partial"), 0o644)).To(Succeed())
		}
		writeLock := filepath.Join(env.State(), "write.lock")
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())

		session, wait := env.StartSync()
		Eventually(session.Err, harness.ExitTimeout).Should(gbytes.Say(regexp.QuoteMeta(
			fmt.Sprintf("lg: waiting for %s (held by pid %d)\n", writeLock, os.Getpid()))))
		for _, leftover := range leftovers {
			Expect(leftover).To(BeAnExistingFile())
		}

		Expect(held.Release()).To(Succeed())
		Expect(wait()).To(gexec.Exit(0))
		Expect(env.Tmp()).To(matchers.BeSwept())
	})
})

var _ = Describe("lg sync with tmp/ a symlink to data/", Label("store"), func() {
	It("refuses to run and keeps data/", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(os.RemoveAll(env.Tmp())).To(Succeed())
		Expect(os.Symlink("data", env.Tmp())).To(Succeed())
		before := treesnap.Snapshot(env.Data())

		session := env.Sync()
		Expect(session).To(gexec.Exit(1))
		Expect(string(session.Err.Contents())).To(ContainSubstring("move LG_HOME as a whole"))
		Expect(treesnap.Snapshot(env.Data())).To(Equal(before))
	})
})

var _ = Describe("lg sync after tmp/ and data/ are removed", Label("store"), func() {
	It("recreates them and exits 0", func() {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(os.RemoveAll(env.Tmp())).To(Succeed())
		Expect(os.RemoveAll(env.Data())).To(Succeed())

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(filepath.Join(env.Data(), fixtureRunDir, "attempt-1")).To(BeADirectory())
	})
})

var _ = Describe("two concurrent lg sync processes", Label("store"), func() {
	It("serialize on state/write.lock: the second waits while fakegithub holds the first's log request, both exit 0, and attempt-1 is published once", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		release := fake.Hold("jobs/111221289888/logs")

		_, waitFirst := env.StartSync()
		Eventually(fake.Requests, harness.ExitTimeout).Should(ContainElement(HaveField("Path", HaveSuffix("jobs/111221289888/logs"))))
		requested := len(fake.Requests())
		second, waitSecond := env.StartSync()
		Eventually(second.Err, harness.ExitTimeout).Should(gbytes.Say("lg: waiting for "))
		Expect(fake.Requests()).To(HaveLen(requested))

		release()
		Expect(waitFirst()).To(gexec.Exit(0))
		Expect(waitSecond()).To(gexec.Exit(0))
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

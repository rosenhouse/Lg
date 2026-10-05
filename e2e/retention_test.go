package e2e_test

import (
	"fmt"
	"io/fs"
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
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// runDirs lists the dirs of a run under every repo dir.
func runDirs(env *harness.Env, id int64) []string {
	GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(env.Data(), "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", id)))
	Expect(err).NotTo(HaveOccurred())
	return dirs
}

// apparentBytes sums the sizes of the regular files under dir.
func apparentBytes(dir string) int64 {
	GinkgoHelper()
	var total int64
	Expect(filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		total += info.Size()
		return err
	})).To(Succeed())
	return total
}

// handMadeRun writes a run dir holding one log under data/, in a store
// that lg would otherwise refuse as not its own.
func handMadeRun(env *harness.Env, rel string) string {
	GinkgoHelper()
	Expect(store.Init(env.Store())).To(Succeed())
	dir := filepath.Join(env.Data(), rel)
	Expect(os.MkdirAll(filepath.Join(dir, "attempt-1"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "attempt-1", "log.txt"), []byte("old log\n"), 0o644)).To(Succeed())
	return dir
}

// writeExtracted writes an extracted/ tree of size bytes into the run's first artifact dir.
func writeExtracted(runDir string, size int) string {
	GinkgoHelper()
	artifacts, err := filepath.Glob(filepath.Join(runDir, "artifacts", "*"))
	Expect(err).NotTo(HaveOccurred())
	Expect(artifacts).NotTo(BeEmpty())
	extracted := filepath.Join(artifacts[0], "extracted")
	Expect(os.MkdirAll(filepath.Join(extracted, "junit"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(extracted, "junit", "report.xml"), []byte(strings.Repeat("x", size)), 0o644)).To(Succeed())
	return extracted
}

func gc(env *harness.Env, args ...string) *gexec.Session {
	GinkgoHelper()
	session := env.Lg(append([]string{"gc"}, args...)...)
	Eventually(session, harness.ExitTimeout).Should(gexec.Exit())
	return session
}

var _ = Describe("lg gc", Label("retention"), func() {
	var (
		env    *harness.Env
		fake   *fakegithub.Server
		synced time.Time
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.Start(fixtureRun, "after-attempt-1")
		synced = harness.DefaultNow().Add(2 * scenario.Day)
		Expect(fake.AddRun(scenario.CloneAt(1, "after-attempt-1", harness.DefaultNow().Add(scenario.Day)))).To(Succeed())
		env.SetNow(synced, fake)
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(runDir(env, fixtureRun)).To(ContainSubstring("/runs/2026-10-03/"))
		Expect(runDir(env, 1)).To(ContainSubstring("/runs/2026-10-04/"))
	})

	Describe("with LG_TEST_NOW 91 days after 2026-10-03", func() {
		It("removes runs/2026-10-03 through tmp/trash and keeps newer date dirs", func() {
			fixture := runDir(env, fixtureRun)
			env.Setenv("LG_TEST_NOW", harness.DefaultNow().Add(91*scenario.Day).Format(time.RFC3339))

			Expect(gc(env)).To(gexec.Exit(0))
			Expect(fixture).NotTo(BeADirectory())
			Expect(filepath.Dir(fixture)).NotTo(BeADirectory())
			Expect(runDirs(env, 1)).To(HaveLen(1))
			Expect(os.ReadDir(filepath.Join(env.Tmp(), "trash"))).To(BeEmpty())
		})
	})

	Describe("with LG_TEST_NOW 90 days after 2026-10-03", func() {
		It("keeps the date dir that sits exactly at the cutoff", func() {
			env.Setenv("LG_TEST_NOW", harness.DefaultNow().Add(90*scenario.Day).Format(time.RFC3339))
			before := treesnap.Snapshot(env.Data())

			Expect(gc(env)).To(gexec.Exit(0))
			Expect(treesnap.Snapshot(env.Data())).To(Equal(before))
		})
	})

	It("removes nothing newer than retention while under disk_cap", func() {
		before := treesnap.Snapshot(env.Data())

		Expect(gc(env)).To(gexec.Exit(0))
		Expect(treesnap.Snapshot(env.Data())).To(Equal(before))
	})
})

var _ = Describe("lg gc --dry-run", Label("retention"), func() {
	It("prints the paths it would remove and removes nothing", func() {
		env := harness.New(lgPath)
		env.WriteConfig("http://127.0.0.1:1")
		older := handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-01/2_ci_main")
		old := handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-02/1_ci_main")
		handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-10-03/3_ci_main")
		before := treesnap.Snapshot(env.Store())

		session := gc(env, "--dry-run")
		Expect(session).To(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(older + "\n" + old + "\n"))
		Expect(treesnap.Snapshot(env.Store())).To(Equal(before))
	})
})

var _ = Describe("lg gc", Label("retention"), func() {
	It("expires runs under every data/<host>/<owner>/<repo>, not only the configured repo", func() {
		env := harness.New(lgPath)
		env.WriteConfig("http://127.0.0.1:1")
		expired := []string{
			handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main"),
			handMadeRun(env, "github.com/other/repo/runs/2026-06-01/2_ci_main"),
			handMadeRun(env, "ghe.example.com/acme/widgets/runs/2026-06-01/3_ci_main"),
		}
		kept := handMadeRun(env, "ghe.example.com/acme/widgets/runs/2026-10-01/4_ci_main")

		Expect(gc(env)).To(gexec.Exit(0))
		for _, dir := range expired {
			Expect(dir).NotTo(BeADirectory())
		}
		Expect(kept).To(BeADirectory())
	})
})

var _ = Describe("lg gc over disk_cap", Label("retention"), func() {
	// Runs 9 and 10 share a date dir, and 10 was created first.
	var (
		env                       *harness.Env
		fake                      *fakegithub.Server
		run11, run10, run9, run12 string
		extracted11, extracted12  string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		created := map[int64]time.Time{
			11: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
			10: time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
			9:  time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
			12: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		}
		for id, at := range created {
			Expect(fake.AddRun(scenario.CloneAt(id, "after-attempt-1", at))).To(Succeed())
		}
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		run11, run10, run9, run12 = runDir(env, 11), runDir(env, 10), runDir(env, 9), runDir(env, 12)
		extracted11, extracted12 = writeExtracted(run11, 1000), writeExtracted(run12, 1000)
	})

	It("removes extracted/ trees, oldest run first, before any run dir", func() {
		env.WriteConfig(fake.URL(), fmt.Sprintf("disk_cap: %d", apparentBytes(env.Data())-1000))

		Expect(gc(env)).To(gexec.Exit(0))
		Expect(extracted11).NotTo(BeADirectory())
		Expect(extracted12).To(BeADirectory())
		for _, dir := range []string{run11, run10, run9, run12} {
			Expect(dir).To(BeADirectory())
		}
	})

	It("then removes whole runs, by date dir and then run id, until data/ is under disk_cap", func() {
		diskCap := apparentBytes(env.Data()) - 2000 - (apparentBytes(run11) - 1000) - apparentBytes(run9)
		env.WriteConfig(fake.URL(), fmt.Sprintf("disk_cap: %d", diskCap))

		Expect(gc(env)).To(gexec.Exit(0))
		Expect(run11).NotTo(BeADirectory())
		Expect(run9).NotTo(BeADirectory())
		Expect(run10).To(BeADirectory())
		Expect(run12).To(BeADirectory())
		Expect(extracted12).NotTo(BeADirectory())
		Expect(apparentBytes(env.Data())).To(BeNumerically("<=", diskCap))
	})
})

var _ = Describe("lg gc while a cycle is publishing", Label("retention"), func() {
	It("waits for the write lock and never evicts a run mid-publish", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		old := handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main")
		release := fake.Hold("jobs/111221289888/logs")
		sync := env.Lg("sync")
		Eventually(fake.Requests, harness.ExitTimeout).Should(ContainElement(HaveField("Path", HaveSuffix("jobs/111221289888/logs"))))

		session := env.Lg("gc")
		Eventually(session.Err, harness.ExitTimeout).Should(gbytes.Say("lg: waiting for "))
		Expect(old).To(BeADirectory())

		release()
		Eventually(sync, harness.ExitTimeout).Should(gexec.Exit(0))
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(env.Data(), fixtureRunDir, "attempt-1")).To(BeADirectory())
		Expect(old).NotTo(BeADirectory())
	})
})

var _ = Describe("lg gc --timeout 1s while another process holds the write lock", Label("retention"), func() {
	It("exits 4 naming the holder's pid", func() {
		env := harness.New(lgPath)
		env.WriteConfig("http://127.0.0.1:1")
		old := handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main")
		held, err := lock.Wait(filepath.Join(env.State(), "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		session := gc(env, "--timeout", "1s")
		Expect(session).To(gexec.Exit(4))
		Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(fmt.Sprintf("held by pid %d", os.Getpid()))))
		Expect(old).To(BeADirectory())
	})
})

// requestsForRun lists the requests that name run id.
func requestsForRun(requests []fakegithub.Request, id int64) []fakegithub.Request {
	named := regexp.MustCompile(fmt.Sprintf(`/runs/%d(/|$)`, id))
	var found []fakegithub.Request
	for _, r := range requests {
		if named.MatchString(r.Path) {
			found = append(found, r)
		}
	}
	return found
}

var _ = Describe("lg sync", Label("retention"), func() {
	var (
		env     *harness.Env
		fake    *fakegithub.Server
		diskCap int64
	)

	// BeforeEach syncs run 1, created 3 days before the fixture run, and then
	// evicts it with lg gc over a disk_cap that fits only the fixture run.
	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.Start(fixtureRun, "after-attempt-1")
		Expect(fake.AddRun(scenario.CloneAt(1, "after-attempt-1", harness.DefaultNow().Add(-3*scenario.Day)))).To(Succeed())
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		diskCap = apparentBytes(env.Data()) - apparentBytes(runDir(env, 1))
		env.WriteConfig(fake.URL(), fmt.Sprintf("disk_cap: %d", diskCap))
		Expect(gc(env)).To(gexec.Exit(0))
		Expect(runDirs(env, 1)).To(BeEmpty())
		Expect(runDirs(env, fixtureRun)).To(HaveLen(1))
	})

	Describe("after cap eviction", func() {
		It("requests nothing for evicted runs and still fetches new attempts of kept runs", func() {
			env.WriteConfig(fake.URL())
			Expect(fake.Advance(fixtureRun, "after-attempt-2")).To(Succeed())
			before := len(fake.Requests())

			Expect(env.Sync()).To(gexec.Exit(0))
			Expect(requestsForRun(fake.Requests()[before:], 1)).To(BeEmpty())
			Expect(runDirs(env, 1)).To(BeEmpty())
			Expect(filepath.Join(runDir(env, fixtureRun), "attempt-2")).To(BeADirectory())
		})
	})

	Describe("after state/horizon.json is deleted", func() {
		It("re-downloads evicted runs inside the backfill window once, evicts them again and rewrites the horizon, and the next sync requests nothing for them", func() {
			horizon := filepath.Join(env.State(), "horizon.json")
			written, err := os.ReadFile(horizon)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Remove(horizon)).To(Succeed())
			before := len(fake.Requests())

			Expect(env.Sync()).To(gexec.Exit(0))
			Expect(requestsForRun(fake.Requests()[before:], 1)).To(ContainElement(HaveField("Path", HaveSuffix("/runs/1/attempts/1"))))
			Expect(runDirs(env, 1)).To(BeEmpty())
			Expect(os.ReadFile(horizon)).To(Equal(written))
			Expect(apparentBytes(env.Data())).To(BeNumerically("<=", diskCap))

			before = len(fake.Requests())
			Expect(env.Sync()).To(gexec.Exit(0))
			Expect(requestsForRun(fake.Requests()[before:], 1)).To(BeEmpty())
		})
	})
})

var _ = Describe("lg sync", Label("retention"), func() {
	It("runs gc after the cycle, also when the cycle is blocked", func() {
		for exit, blocked := range map[int]bool{0: false, 3: true} {
			env := harness.New(lgPath)
			fake := fakegithub.New()
			DeferCleanup(fake.Close)
			env.WriteConfig(fake.URL())
			old := handMadeRun(env, "github.com/rosenhouse/Lg/runs/2026-06-01/1_ci_main")
			if blocked {
				env.GH().Fail("no oauth token found for github.com")
			}

			session := env.Lg("sync")
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(exit), "blocked %t", blocked)
			Expect(old).NotTo(BeADirectory(), "blocked %t", blocked)
		}
	})
})

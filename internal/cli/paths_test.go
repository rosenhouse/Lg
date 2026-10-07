package cli_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// fixtureRun is the run harness.CLI serves.
const fixtureRun = 37129390741

var _ = Describe("lg paths", Label("sync"), func() {
	var (
		home           string
		stdout, stderr *bytes.Buffer
	)

	BeforeEach(func() {
		home = filepath.Join(GinkgoT().TempDir(), "lg")
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	})

	paths := func() int {
		return cli.Main([]string{"paths"}, cli.Deps{Env: map[string]string{"LG_HOME": home}, Stdout: stdout, Stderr: stderr, Clock: clock.Real{}})
	}

	It("prints nothing and exits 0 before the first sync creates data/", func() {
		Expect(paths()).To(Equal(0))
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(Equal("lg: warning: never synced; run `lg sync`\n"))
	})

	It("exits 1 naming data/ when data/ is a dangling symlink", func() {
		Expect(os.MkdirAll(home, 0o755)).To(Succeed())
		data := filepath.Join(home, "data")
		Expect(os.Symlink(filepath.Join(home, "unmounted"), data)).To(Succeed())

		Expect(paths()).To(Equal(1))
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(ContainSubstring(data))
	})
})

var _ = Describe("lg paths", Label("sync"), func() {
	var c *harness.CLI

	BeforeEach(func() {
		c = harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
	})

	logs := func(data string) []string {
		GinkgoHelper()
		found, err := filepath.Glob(filepath.Join(data, "*", "*", "*", "runs", "*", "*", "attempt-1", "jobs", "*", "log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(HaveLen(10))
		return found
	}

	printed := func() []string {
		GinkgoHelper()
		return strings.Fields(c.Stdout.String())
	}

	It("prints only regular log.txt files under data/, never under tmp/", func() {
		data := filepath.Join(c.Home, "data")
		all := logs(data)
		Expect(os.Remove(all[0])).To(Succeed())
		Expect(os.Symlink(all[2], all[0])).To(Succeed())
		Expect(os.Remove(all[1])).To(Succeed())
		Expect(os.Mkdir(all[1], 0o755)).To(Succeed())
		rel, err := filepath.Rel(data, all[2])
		Expect(err).NotTo(HaveOccurred())
		staged := filepath.Join(c.Home, "tmp", "unit-1", rel)
		Expect(os.MkdirAll(filepath.Dir(staged), 0o755)).To(Succeed())
		Expect(os.WriteFile(staged, []byte("log"), 0o644)).To(Succeed())

		Expect(c.Main("paths")).To(Equal(0))
		Expect(printed()).To(ConsistOf(all[2:]))
	})

	It("prints paths under data/ when data/ is a symlink", func() {
		elsewhere := filepath.Join(GinkgoT().TempDir(), "data")
		Expect(os.Rename(filepath.Join(c.Home, "data"), elsewhere)).To(Succeed())
		Expect(os.Symlink(elsewhere, filepath.Join(c.Home, "data"))).To(Succeed())

		Expect(c.Main("paths")).To(Equal(0))
		Expect(printed()).To(ConsistOf(logs(filepath.Join(c.Home, "data"))))
	})
})

var _ = DescribeTable("lg paths --since and --until, relative to LG_TEST_NOW 2026-10-03T18:00:00Z, of attempt 1, which started at 2026-10-03T14:22:54Z", Label("paths"),
	func(flag, value string, listed bool) {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))

		Expect(c.Main("paths", flag, value)).To(Equal(0), c.Stderr.String())
		if listed {
			Expect(strings.Fields(c.Stdout.String())).To(HaveLen(10))
		} else {
			Expect(c.Stdout.String()).To(BeEmpty())
		}
	},
	Entry("30d", "--since", "30d", true),
	Entry("3h", "--since", "3h", false),
	Entry("4h", "--since", "4h", true),
	Entry("a duration back to a second after the start", "--since", "3h37m5s", false),
	Entry("a duration back to the start, inclusive", "--since", "3h37m6s", true),
	Entry("a date, at UTC midnight", "--since", "2026-10-03", true),
	Entry("the next date", "--since", "2026-10-04", false),
	Entry("a date, until UTC midnight", "--until", "2026-10-03", false),
	Entry("an RFC 3339 time, inclusive", "--until", "2026-10-03T14:22:54Z", true),
	Entry("an RFC 3339 time in another zone, a second before the start", "--until", "2026-10-03T16:22:53+02:00", false),
	Entry("an RFC 3339 time in another zone, at the start", "--until", "2026-10-03T16:22:54+02:00", true),
	Entry("a duration back to before the start", "--until", "4h", false),
	Entry("a duration back to after the start", "--until", "3h", true),
)

var _ = DescribeTable("lg paths takes a comma as part of the value of", Label("paths"),
	func(flag, value string, printed types.GomegaMatcher) {
		c := harness.NewCLI()
		comma := scenario.RenameWorkflow(scenario.OnBranch(scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), 1), "fix,retry"), 1, "ci, nightly")
		Expect(c.Fake.AddRun(comma)).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))

		Expect(c.Main("paths", flag, value)).To(Equal(0))
		Expect(strings.Fields(c.Stdout.String())).To(printed)
	},
	Entry("--branch fix,retry", "--branch", "fix,retry", And(HaveLen(10), HaveEach(ContainSubstring("/1_")))),
	Entry("--workflow 'ci, nightly'", "--workflow", "ci, nightly", HaveLen(20)),
	Entry("--job 'build (ubuntu-latest, 1.22)'", "--job", "build (ubuntu-latest, 1.22)", And(HaveLen(2), HaveEach(HaveSuffix("_build-ubuntu-latest-1.22/log.txt")))),
)

var _ = DescribeTable("lg paths exits 2", Label("paths"),
	func(args []string, message string) {
		c := harness.NewCLI()

		Expect(c.Main(append([]string{"paths"}, args...)...)).To(Equal(2))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(ContainSubstring(message))
	},
	Entry("for an unknown --unit", []string{"--unit", "step"}, `--unit must be one of "run","attempt","job","log","artifact","extracted" but got "step"`),
	Entry("for a --since that is no time", []string{"--since", "yesterday"}, `--since: want 30d, 12h, 2026-09-01 or an RFC 3339 time, not "yesterday"`),
	Entry("for an --until in the future", []string{"--until=-1h"}, `--until: want 30d, 12h, 2026-09-01 or an RFC 3339 time, not "-1h"`),
	Entry("for an --until before 1970, which no run is", []string{"--until", "0001-01-01"}, `--until: want a time since 1970, not "0001-01-01"`),
	Entry("for a --since before 1970", []string{"--since", "1969-12-31T23:59:59Z"}, `--since: want a time since 1970, not "1969-12-31T23:59:59Z"`),
	Entry("for a --pr that is no number", []string{"--pr", "x"}, `--pr`),
	Entry("for an empty --branch", []string{"--branch", ""}, "--branch must not be empty"),
	Entry("for an empty --sha, which would match every run", []string{"--sha", "main", "--sha", ""}, "--sha must not be empty"),
	Entry("for an empty --workflow", []string{"--workflow="}, "--workflow must not be empty"),
	Entry("for an empty --job", []string{"--job", ""}, "--job must not be empty"),
	Entry("for a --job with an unclosed [, which would match no job", []string{"--job", "build", "--job", "flak[y"}, `--job "flak[y": unclosed [`),
	Entry("for a --job whose [ holds only ], which a class takes as a member", []string{"--job", "x[]"}, `--job "x[]": unclosed [`),
	Entry("for a --job whose [ holds only ^]", []string{"--job", "x[^]"}, `--job "x[^]": unclosed [`),
	Entry("for an empty --event", []string{"--event", ""}, "--event must not be empty"),
	Entry("for an empty --conclusion", []string{"--conclusion", ""}, "--conclusion must not be empty"),
	Entry("for a repeated --unit", []string{"--unit", "log", "--unit", "extracted"}, "--unit must not be given more than once"),
	Entry("for a repeated --since", []string{"--since", "60d", "--since", "1d"}, "--since: must not be given more than once"),
	Entry("for a repeated --until", []string{"--until", "60d", "--until=1d"}, "--until: must not be given more than once"),
)

var _ = DescribeTable("lg paths prints the paths it can read, then exits 1 naming what it cannot", Label("paths"),
	// spoil spoils the files of the run at runDir, and gives the path stderr
	// names and the dir whose logs drop out.
	func(spoil func(c *harness.CLI, runDir string) (named, gone string)) {
		c := harness.NewCLI()
		Expect(c.Fake.AddRun(scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), 1))).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))
		Expect(c.Main("paths")).To(Equal(0))
		all := strings.Fields(c.Stdout.String())
		Expect(all).To(HaveLen(20))
		runDirs, err := filepath.Glob(filepath.Join(c.Home, "data", "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", fixtureRun)))
		Expect(err).NotTo(HaveOccurred())
		Expect(runDirs).To(HaveLen(1))
		named, gone := spoil(c, runDirs[0])

		Expect(c.Main("paths")).To(Equal(1))
		Expect(strings.Fields(c.Stdout.String())).To(Equal(slices.DeleteFunc(all, func(path string) bool {
			return strings.HasPrefix(path, gone+string(filepath.Separator))
		})))
		Expect(c.Stderr.String()).To(ContainSubstring(named))
	},
	Entry("a run whose attempt.json does not parse, read into a new lg.db", func(c *harness.CLI, runDir string) (string, string) {
		attempt := filepath.Join(runDir, "attempt-1", "attempt.json")
		Expect(os.WriteFile(attempt, []byte("{"), 0o644)).To(Succeed())
		dbs, err := filepath.Glob(filepath.Join(c.Home, "state", "lg.db*"))
		Expect(err).NotTo(HaveOccurred())
		for _, db := range dbs {
			Expect(os.Remove(db)).To(Succeed())
		}
		return attempt, runDir
	}),
	Entry("a job dir that is now a file", func(c *harness.CLI, runDir string) (string, string) {
		jobs, err := filepath.Glob(filepath.Join(runDir, "attempt-1", "jobs", "*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.RemoveAll(jobs[0])).To(Succeed())
		Expect(os.WriteFile(jobs[0], nil, 0o644)).To(Succeed())
		return jobs[0], jobs[0]
	}),
)

var _ = DescribeTable("lg says that it waits for lg.db.lock, and whom for", Label("paths"),
	func(args ...string) {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		dbLock := filepath.Join(c.Home, "state", "lg.db.lock")
		held, err := lock.Wait(dbLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		// A failed assertion leaves no lg waiting out the lock past the spec.
		DeferCleanup(func() { _ = held.Release() })
		stderr := gbytes.NewBuffer()
		code := make(chan int, 1)
		go func() {
			code <- cli.Main(args, cli.Deps{Env: map[string]string{"LG_HOME": c.Home}, Stdout: io.Discard, Stderr: stderr, Clock: clock.Real{}})
		}()

		Eventually(stderr, 5*time.Second).Should(gbytes.Say(regexp.QuoteMeta(fmt.Sprintf("lg: waiting for %s (held by pid %d)\n", dbLock, os.Getpid()))))
		Expect(held.Release()).To(Succeed())
		Eventually(code, 5*time.Second).Should(Receive(Equal(0)))
	},
	Entry("lg paths", "paths"),
	Entry("lg index rebuild", "index", "rebuild"),
)

var _ = Describe("lg paths", Label("paths"), func() {
	It("says nothing of a wait for lg.db.lock shorter than a second", func() {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		held, err := lock.Wait(filepath.Join(c.Home, "state", "lg.db.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		// A failed assertion leaves no lg waiting out the lock past the spec.
		DeferCleanup(func() { _ = held.Release() })
		stderr := gbytes.NewBuffer()
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"paths"}, cli.Deps{Env: map[string]string{"LG_HOME": c.Home}, Stdout: io.Discard, Stderr: stderr, Clock: clock.Real{}})
		}()

		<-clock.Real{}.After(300 * time.Millisecond)
		Expect(held.Release()).To(Succeed())
		Eventually(code, 5*time.Second).Should(Receive(Equal(0)))
		Expect(string(stderr.Contents())).NotTo(ContainSubstring("lg.db.lock"))
	})
})

var _ = Describe("lg paths", Label("paths"), func() {
	It("answers from an index of data/ in memory, saying why, when lg.db cannot be opened", func() {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		Expect(c.Main("paths")).To(Equal(0))
		want := c.Stdout.String()
		db := filepath.Join(c.Home, "state", "lg.db")
		for _, suffix := range []string{"", "-wal", "-shm"} {
			Expect(os.RemoveAll(db + suffix)).To(Succeed())
		}
		Expect(os.Mkdir(db, 0o755)).To(Succeed())

		Expect(c.Main("paths")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(want))
		Expect(c.Stderr.String()).To(MatchRegexp(`^lg: warning: indexing data/ in memory, since %s: .+\n$`, regexp.QuoteMeta(db)))
	})
})

var _ = Describe("lg paths", Label("paths"), func() {
	It("names a path holding a newline instead of printing it, unless -0, and exits 1", func() {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		artifacts, err := filepath.Glob(filepath.Join(c.Home, "data", "*", "*", "*", "runs", "*", "*", "artifacts", "*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(artifacts).NotTo(BeEmpty())
		extracted := filepath.Join(artifacts[0], "extracted")
		plain, split := filepath.Join(extracted, "plain.txt"), filepath.Join(extracted, "new\nline.txt")
		Expect(os.MkdirAll(extracted, 0o755)).To(Succeed())
		for _, file := range []string{plain, split} {
			Expect(os.WriteFile(file, []byte("foo bar\n"), 0o644)).To(Succeed())
		}

		Expect(c.Main("paths", "--unit", "extracted")).To(Equal(1))
		Expect(c.Stdout.String()).To(Equal(plain + "\n"))
		Expect(c.Stderr.String()).To(ContainSubstring(fmt.Sprintf("%q holds a newline; use -0", split)))

		Expect(c.Main("paths", "--unit", "extracted", "-0")).To(Equal(0), c.Stderr.String())
		Expect(strings.Split(c.Stdout.String(), "\x00")).To(ConsistOf(plain, split, ""))
	})
})

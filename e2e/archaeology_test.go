package e2e_test

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// archaeologyEnv syncs the Archaeology runs and any others with backfill 60d.
func archaeologyEnv(others ...scenario.Run) *harness.Env {
	GinkgoHelper()
	return syncArchaeology(harness.New(lgPath), others...)
}

func syncArchaeology(env *harness.Env, others ...scenario.Run) *harness.Env {
	GinkgoHelper()
	fake := fakegithub.New()
	DeferCleanup(fake.Close)
	for _, r := range append(scenario.Archaeology().All(), others...) {
		Expect(fake.AddRun(r)).To(Succeed())
	}
	env.WriteConfig(fake.URL(), "backfill: 60d")
	Expect(env.Sync()).To(gexec.Exit(0))
	return env
}

// runDirOf is the dir of the run under data/.
func runDirOf(env *harness.Env, id int64) string {
	GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(env.Data(), "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", id)))
	Expect(err).NotTo(HaveOccurred())
	Expect(dirs).To(HaveLen(1))
	return dirs[0]
}

func glob(pattern ...string) []string {
	GinkgoHelper()
	paths, err := filepath.Glob(filepath.Join(pattern...))
	Expect(err).NotTo(HaveOccurred())
	Expect(paths).NotTo(BeEmpty(), filepath.Join(pattern...))
	return paths
}

// passLog is the log of job "pass" in the run's attempt 1.
func passLog(env *harness.Env, id int64) string {
	GinkgoHelper()
	logs := glob(runDirOf(env, id), "attempt-1", "jobs", "*_pass", "log.txt")
	Expect(logs).To(HaveLen(1))
	return logs[0]
}

// extracted is a tree that lg extract could have written beside the first
// artifact of Release3, holding FooBar in a file and in .lg-extract.json.
type extracted struct{ dir, file, manifest string }

func extractByHand(env *harness.Env) extracted {
	GinkgoHelper()
	artifact := glob(runDirOf(env, scenario.Archaeology().Release3.ID), "artifacts", "*")[0]
	e := extracted{
		dir:      filepath.Join(artifact, "extracted"),
		file:     filepath.Join(artifact, "extracted", "report.zip.d", "logs", "test.log"),
		manifest: filepath.Join(artifact, "extracted", ".lg-extract.json"),
	}
	Expect(os.MkdirAll(filepath.Dir(e.file), 0o755)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(e.dir, "empty"), 0o755)).To(Succeed())
	Expect(os.WriteFile(e.file, []byte("--- FAIL: "+scenario.FooBar+"\n"), 0o644)).To(Succeed())
	Expect(os.WriteFile(e.manifest, []byte(`{"renamed": [`+fmt.Sprintf("%q", scenario.FooBar)+`]}`+"\n"), 0o644)).To(Succeed())
	Expect(os.Symlink(passLog(env, scenario.Archaeology().Release3.ID), filepath.Join(e.dir, "link.txt"))).To(Succeed())
	return e
}

// lines runs lg with args to exit 0 and gives the lines it printed.
func lines(env *harness.Env, args ...string) []string {
	GinkgoHelper()
	session := env.Lg(args...)
	Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
	return outputLines(session)
}

func outputLines(session *gexec.Session) []string {
	out := strings.TrimSuffix(string(session.Out.Contents()), "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func under(dir string) types.GomegaMatcher {
	return HavePrefix(dir + string(filepath.Separator))
}

// These specs only read the store, so they share one sync.
var _ = Describe("lg paths over the synced Archaeology runs", Ordered, Label("paths"), func() {
	var (
		env *harness.Env
		e   extracted
		a   = scenario.Archaeology()
	)

	BeforeAll(func() {
		env = archaeologyEnv()
		e = extractByHand(env)
	})

	Describe("W1: foo bar on main or release-3 in the last 30 days", func() {
		w1 := "lg paths --branch main --branch release-3 --since 30d -0 | xargs -0 -r grep -l 'foo bar'"

		It("exits 0 listing the 2026-09-10 main log, the 2026-09-20 release-3 log and the file under the release-3 artifact's extracted/ tree", func() {
			grep := env.Sh(w1)
			Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(outputLines(grep)).To(ConsistOf(passLog(env, a.MainSeptember.ID), passLog(env, a.Release3.ID), e.file))
		})

		It("never lists the fork pull_request run whose head branch is main", func() {
			fork := runDirOf(env, a.Fork.ID)
			Expect(lines(env, "paths", "--event", "pull_request")).To(ContainElement(under(fork)))

			main := lines(env, "paths", "--branch", "main")
			Expect(main).To(ContainElement(under(runDirOf(env, a.MainSeptember.ID))))
			Expect(main).NotTo(ContainElement(under(fork)))
		})

		It("never lists the run with 'foo bar' only in its display_title, because the default unit excludes JSON", func() {
			titleOnly := runDirOf(env, a.TitleOnly.ID)
			Expect(lines(env, "paths", "--branch", "main", "--since", "30d")).To(ContainElement(under(titleOnly)))

			grep := env.Sh(w1)
			Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(outputLines(grep)).NotTo(ContainElement(under(titleOnly)))
		})
	})

	Describe("W3: what happened on a SHA or a PR", func() {
		It("`lg paths --sha <prefix> --unit attempt` and `lg paths --pr 42 --unit attempt` list the JSON files of each matching attempt, and jq over their attempt.json files prints one line per attempt", func() {
			files := func(run string, attempts ...string) []string {
				var all []string
				for _, attempt := range attempts {
					for _, name := range []string{"attempt.json", "jobs.json", "artifacts.json", "fetch.json"} {
						all = append(all, filepath.Join(run, attempt, name))
					}
				}
				return all
			}

			Expect(lines(env, "paths", "--sha", "8888", "--unit", "attempt")).To(ConsistOf(files(runDirOf(env, a.Rerun.ID), "attempt-1", "attempt-2")))
			Expect(lines(env, "paths", "--pr", "42", "--unit", "attempt")).To(ConsistOf(files(runDirOf(env, a.PR42.ID), "attempt-1")))

			jq := `| grep 'attempt.json$' | xargs jq -r '[.id, .run_attempt, .conclusion] | @tsv'`
			bySHA := env.Sh("lg paths --sha 8888 --unit attempt " + jq)
			Eventually(bySHA, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(outputLines(bySHA)).To(ConsistOf("8\t1\tfailure", "8\t2\tsuccess"))
			byPR := env.Sh("lg paths --pr 42 --unit attempt " + jq)
			Eventually(byPR, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(outputLines(byPR)).To(ConsistOf("7\t1\tfailure"))
		})
	})

	Describe("lg paths", func() {
		It("prints only regular files, expanding extracted/ trees, so grep -l never sees a directory", func() {
			printed := lines(env, "paths")
			Expect(printed).To(ContainElement(e.file))
			for _, path := range printed {
				info, err := os.Lstat(path)
				Expect(err).NotTo(HaveOccurred())
				Expect(info.Mode().IsRegular()).To(BeTrue(), path)
			}
			grep := env.Sh("lg paths -0 | xargs -0 -r grep -l 'foo bar'")
			Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(grep.Err.Contents()).To(BeEmpty())
		})

		It("leaves extracted/.lg-extract.json out of the default unit and out of --unit extracted", func() {
			for _, args := range [][]string{{"paths"}, {"paths", "--unit", "extracted"}} {
				printed := lines(env, args...)
				Expect(printed).To(ContainElement(e.file), "%v", args)
				Expect(printed).NotTo(ContainElement(e.manifest), "%v", args)
			}
		})

		It("combines --sha prefix, --pr, --workflow, --job glob, --event, --conclusion, --since and --until with AND across flags and OR within a repeated flag", func() {
			matrixJobs := func(id int64) []string {
				return glob(runDirOf(env, id), "attempt-1", "jobs", "*_matrix-1.2?", "job.json")
			}
			paths := func(replaced ...string) []string {
				flags := map[string][]string{
					"--sha": {"3333", "7777"}, "--pr": {"42"}, "--workflow": {"lg-fixture"}, "--job": {"matrix*"},
					"--event": {"push", "pull_request"}, "--conclusion": {"success"},
					"--since": {"2026-09-15"}, "--until": {"2026-09-30T00:00:00Z"},
				}
				for i := 0; i < len(replaced); i += 2 {
					flags[replaced[i]] = strings.Fields(replaced[i+1])
				}
				args := []string{"paths", "--unit", "job"}
				for _, flag := range slices.Sorted(maps.Keys(flags)) {
					for _, v := range flags[flag] {
						args = append(args, flag, v)
					}
				}
				return lines(env, args...)
			}

			Expect(matrixJobs(a.PR42.ID)).To(HaveLen(2))
			Expect(paths()).To(ConsistOf(matrixJobs(a.PR42.ID)))
			Expect(paths("--pr", "")).To(ConsistOf(append(matrixJobs(a.PR42.ID), matrixJobs(a.Release3.ID)...)))
			Expect(paths("--pr", "", "--event", "push")).To(ConsistOf(matrixJobs(a.Release3.ID)))
			Expect(paths("--pr", "", "--sha", "3333")).To(ConsistOf(matrixJobs(a.Release3.ID)))
			Expect(paths("--until", "2026-09-27")).To(BeEmpty())
			Expect(paths("--since", "2026-09-29")).To(BeEmpty())
			Expect(paths("--conclusion", "failure")).To(BeEmpty())
			Expect(paths("--workflow", "ci")).To(BeEmpty())
			Expect(paths("--job", "matrix")).To(BeEmpty())
			Expect(paths("--pr", "43")).To(BeEmpty())
		})

		It("prints nothing and exits 0 when nothing matches", func() {
			Expect(lines(env, "paths", "--branch", "no-such-branch")).To(BeEmpty())
		})
	})

	Describe("lg paths --since 30d --unit log", func() {
		It("lists the attempt-2 logs of a run created 31 days ago and re-run 2 days ago, but not its attempt-1 logs", func() {
			rerun := runDirOf(env, a.Rerun.ID)

			var listed []string
			for _, path := range lines(env, "paths", "--since", "30d", "--unit", "log") {
				if strings.HasPrefix(path, rerun+string(filepath.Separator)) {
					listed = append(listed, path)
				}
			}
			Expect(listed).To(ConsistOf(glob(rerun, "attempt-2", "jobs", "*", "log.txt")))
		})
	})

	DescribeTable("lg paths --unit",
		func(unit string, want func() []string) {
			expected := want()
			Expect(expected).NotTo(BeEmpty())
			Expect(lines(env, "paths", "--unit", unit)).To(ConsistOf(expected))
		},
		Entry("run: every .json file under the run dir outside extracted/", "run", func() []string {
			return filesUnder(env.Data(), func(path string) bool {
				return strings.HasSuffix(path, ".json") && !strings.Contains(path, "/extracted/")
			})
		}),
		Entry("attempt: attempt.json, jobs.json, artifacts.json and fetch.json", "attempt", func() []string {
			attempts := glob(env.Data(), "*", "*", "*", "runs", "*", "*", "attempt-*")
			var files []string
			for _, attempt := range attempts {
				for _, name := range []string{"attempt.json", "jobs.json", "artifacts.json", "fetch.json"} {
					files = append(files, filepath.Join(attempt, name))
				}
			}
			return files
		}),
		Entry("job: job.json", "job", func() []string {
			return glob(env.Data(), "*", "*", "*", "runs", "*", "*", "attempt-*", "jobs", "*", "job.json")
		}),
		Entry("log: log.txt", "log", func() []string {
			return glob(env.Data(), "*", "*", "*", "runs", "*", "*", "attempt-*", "jobs", "*", "log.txt")
		}),
		Entry("artifact: artifact.zip", "artifact", func() []string {
			return glob(env.Data(), "*", "*", "*", "runs", "*", "*", "artifacts", "*", "artifact.zip")
		}),
		Entry("extracted: the regular files under extracted/", "extracted", func() []string {
			return filesUnder(e.dir, func(path string) bool { return path != e.manifest })
		}),
	)
})

var _ = Describe("lg paths", Label("paths"), func() {
	It("never lists carried-forward jobs or tombstones as logs, and omits paths that no longer exist", func() {
		env := archaeologyEnv()
		carriedForward := slices.DeleteFunc(glob(runDirOf(env, scenario.Archaeology().Rerun.ID), "attempt-2", "jobs", "*"), func(job string) bool {
			return slices.ContainsFunc([]string{"log.txt", "log.txt.tombstone"}, func(name string) bool {
				_, err := os.Lstat(filepath.Join(job, name))
				return err == nil
			})
		})
		Expect(carriedForward).NotTo(BeEmpty())
		Expect(glob(env.Data(), "*", "*", "*", "runs", "*", "*", "attempt-*", "jobs", "*", "log.txt.tombstone")).NotTo(BeEmpty())
		logs := glob(env.Data(), "*", "*", "*", "runs", "*", "*", "attempt-*", "jobs", "*", "log.txt")
		Expect(lines(env, "paths", "--unit", "log")).To(ConsistOf(logs))

		Expect(os.Remove(logs[0])).To(Succeed())
		Expect(lines(env, "paths", "--unit", "log")).To(ConsistOf(logs[1:]))
	})

	It("reconciles before answering, so a unit published a moment ago is listed and a deleted lg.db is rebuilt", func() {
		env := archaeologyEnv()
		logs := lines(env, "paths", "--unit", "log")
		run := runDirOf(env, scenario.Archaeology().Release3.ID)
		aside := filepath.Join(env.Tmp(), filepath.Base(run))
		Expect(os.Rename(run, aside)).To(Succeed())
		Expect(env.Lg("index", "rebuild").Wait(harness.ExitTimeout)).To(gexec.Exit(0))

		Expect(os.Rename(aside, run)).To(Succeed())
		Expect(lines(env, "paths", "--unit", "log")).To(ConsistOf(logs))

		for _, suffix := range []string{"", "-wal", "-shm"} {
			Expect(os.RemoveAll(filepath.Join(env.State(), "lg.db"+suffix))).To(Succeed())
		}
		Expect(lines(env, "paths", "--unit", "log")).To(ConsistOf(logs))
		Expect(filepath.Join(env.State(), "lg.db")).To(BeARegularFile())
	})
})

var _ = Describe("lg paths --unit artifact", Label("paths"), func() {
	It("lists an artifact of a run whose attempt is still pending, also when filtered by --workflow, --event and --pr", func() {
		running := scenario.CloneAt(9, "after-attempt-1", harness.DefaultNow().Add(-time.Hour))
		running = scenario.WithPullRequests(scenario.WithEvent(scenario.InProgress(running, 1), "pull_request"), 43)
		env := archaeologyEnv(running)
		Expect(filepath.Glob(filepath.Join(runDirOf(env, 9), "attempt-*"))).To(BeEmpty())
		zips := glob(runDirOf(env, 9), "artifacts", "*", "artifact.zip")

		Expect(lines(env, "paths", "--unit", "artifact")).To(ContainElements(zips))
		Expect(lines(env, "paths", "--unit", "artifact", "--workflow", "lg-fixture", "--event", "pull_request", "--pr", "43")).To(ConsistOf(zips))
	})
})

// filesUnder lists the regular files below dir that keep accepts.
func filesUnder(dir string, keep func(path string) bool) []string {
	GinkgoHelper()
	var found []string
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && keep(path) {
			found = append(found, path)
		}
		return err
	})).To(Succeed())
	return found
}

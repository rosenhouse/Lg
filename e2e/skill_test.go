package e2e_test

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/doctest"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
	skill "github.com/rosenhouse/lg/skill/lg"
)

var _ = Describe("lg skill install", Label("skill"), func() {
	It("writes ${CLAUDE_CONFIG_DIR:-~/.claude}/skills/lg/SKILL.md identical to the embedded copy, replacing an older copy and printing the path", func() {
		env := harness.New(lgPath)
		installsTo := func(path string) {
			GinkgoHelper()
			session := env.Lg("skill", "install")
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(outputLines(session)).To(Equal([]string{path}))
			Expect(os.ReadFile(path)).To(Equal([]byte(skill.Markdown)))
		}

		installsTo(filepath.Join(env.Home(), ".claude", "skills", "lg", "SKILL.md"))

		claude := GinkgoT().TempDir()
		env.Setenv("CLAUDE_CONFIG_DIR", claude)
		older := filepath.Join(claude, "skills", "lg", "SKILL.md")
		Expect(os.MkdirAll(filepath.Dir(older), 0o755)).To(Succeed())
		Expect(os.WriteFile(older, []byte("an older copy\n"), 0o644)).To(Succeed())
		installsTo(older)
	})

	DescribeTable("installs when lg cannot use the store",
		func(storeEnv func(env *harness.Env)) {
			env := harness.New(lgPath)
			claude := GinkgoT().TempDir()
			env.Setenv("CLAUDE_CONFIG_DIR", claude)
			storeEnv(env)

			session := env.Lg("skill", "install")

			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
			Expect(os.ReadFile(filepath.Join(claude, "skills", "lg", "SKILL.md"))).To(Equal([]byte(skill.Markdown)))
		},
		Entry("a store dir holding files lg did not write", func(env *harness.Env) {
			foreign := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("hi\n"), 0o644)).To(Succeed())
			env.Setenv("LG_HOME", foreign)
		}),
		Entry("no HOME or LG_HOME", func(env *harness.Env) { env.Setenv("HOME", "") }),
		Entry("a relative LG_HOME", func(env *harness.Env) { env.Setenv("LG_HOME", "relative") }),
	)
})

// These specs only read the store, so they share one sync and extract.
var _ = Describe("SKILL.md", Ordered, ContinueOnFailure, Label("skill"), func() {
	var env *harness.Env

	BeforeAll(func() {
		env = harness.New(lgPath)
		bin := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(bin, "sqlite3"), []byte("#!/bin/sh\necho 'sqlite3 is unavailable' >&2\nexit 127\n"), 0o755)).To(Succeed())
		env.PrependPath(bin)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		for _, r := range scenario.Archaeology().All() {
			Expect(fake.AddRun(r)).To(Succeed())
		}
		env.WriteConfig(fake.URL(), "backfill: 60d")
		Expect(env.Sync()).To(gexec.Exit(0))
		for _, stage := range []string{"after-attempt-2", "after-attempt-3"} {
			Expect(fake.Advance(fixtureRun, stage)).To(Succeed())
			Expect(env.Sync()).To(gexec.Exit(0))
		}
		Expect(extract(env, "--all")).To(gexec.Exit(0))
	})

	It("runs every ```sh block with /bin/bash -euo pipefail against a store synced from the recordings and Archaeology() and then extracted, each exiting 0, on Linux and on macOS (bash 3.2, BSD tools, no sqlite3)", func() {
		blocks := doctest.ShBlocks(skill.Markdown)
		Expect(blocks).NotTo(BeEmpty())
		for _, b := range blocks {
			session := env.Bash(b.Text)
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit(), "line %d", b.Line)
			Expect(session.ExitCode()).To(Equal(0), "the block on line %d:\n%s\nprinted:\n%s%s", b.Line, b.Text, session.Out.Contents(), session.Err.Contents())
		}
	})

	It("names every file kind a sync and an extract write (attempt.json, jobs.json, artifacts.json, fetch.json, job.json, log.txt, *.tombstone, artifact.json, artifact.zip, extracted/)", func() {
		kinds := map[string]bool{}
		Expect(filepath.WalkDir(env.Data(), func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.Name() == "extracted":
				kinds["extracted/"] = true
				return filepath.SkipDir
			case strings.HasSuffix(d.Name(), ".tombstone"):
				kinds["*.tombstone"] = true
			case d.Type().IsRegular():
				kinds[d.Name()] = true
			}
			return nil
		})).To(Succeed())
		Expect(slices.Collect(maps.Keys(kinds))).To(ConsistOf(
			"attempt.json", "jobs.json", "artifacts.json", "fetch.json", "job.json", "log.txt",
			"*.tombstone", "artifact.json", "artifact.zip", "extracted/"))
		for kind := range kinds {
			Expect(skill.Markdown).To(ContainSubstring(strings.TrimPrefix(kind, "*")), kind)
		}
	})
})

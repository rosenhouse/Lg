package e2e_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// These specs only read the store, so they share one sync and extract.
var _ = Describe("lg grep over the synced and extracted Archaeology runs", Ordered, Label("grep"), func() {
	var env *harness.Env

	BeforeAll(func() {
		env = archaeologyEnv()
		Expect(extract(env, "--all")).To(gexec.Exit(0))
	})

	// w1 runs lg grep, or lg paths piped to rg, with W1's filters.
	w1 := func(script string) *gexec.Session {
		GinkgoHelper()
		session := env.Sh(script)
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit())
		return session
	}
	const filters = "--branch main --branch release-3 --since 30d"

	It("prints for W1 what lg paths piped to rg -Hn prints: each hit as path:line:text, oldest first", func() {
		rg := w1("lg paths " + filters + " -0 | xargs -0 rg --no-config -j1 -Hn 'foo bar'")
		Expect(rg).To(gexec.Exit(0))
		Expect(outputLines(rg)).To(HaveLen(3))

		grep := w1("lg grep " + filters + " 'foo bar'")
		Expect(grep).To(gexec.Exit(0))
		Expect(string(grep.Out.Contents())).To(Equal(string(rg.Out.Contents())))
		Expect(grep.Err.Contents()).To(BeEmpty())
	})

	It("prints with -l each file that rg -l prints", func() {
		rg := w1("lg paths " + filters + " -0 | xargs -0 rg --no-config -j1 -l 'foo bar'")
		Expect(rg).To(gexec.Exit(0))

		grep := w1("lg grep -l " + filters + " 'foo bar'")
		Expect(grep).To(gexec.Exit(0))
		Expect(string(grep.Out.Contents())).To(Equal(string(rg.Out.Contents())))
	})

	It("prints with --json what lg where prints of its hits, and with -l --json of its files", func() {
		for _, flags := range []string{"", "-l "} {
			where := w1("lg grep " + flags + filters + " 'foo bar' | lg where")
			Expect(where).To(gexec.Exit(0))
			Expect(outputLines(where)).To(HaveLen(3))

			grep := w1("lg grep --json " + flags + filters + " 'foo bar'")
			Expect(grep).To(gexec.Exit(0))
			Expect(string(grep.Out.Contents())).To(Equal(string(where.Out.Contents())), flags)
		}
	})

	It("exits 5 and says on stderr how many files it searched when no line matches", func() {
		files := len(lines(env, "paths", "--branch", "main"))
		Expect(files).To(BeNumerically(">", 1))

		grep := w1("lg grep --branch main 'no such text'")
		Expect(grep).To(gexec.Exit(5))
		Expect(grep.Out.Contents()).To(BeEmpty())
		Expect(string(grep.Err.Contents())).To(Equal(fmt.Sprintf("lg: no match in %d files\n", files)))

		none := w1("lg grep --branch no-such-branch '" + scenario.FooBar + "'")
		Expect(none).To(gexec.Exit(5))
		Expect(string(none.Err.Contents())).To(Equal("lg: no match in 0 files\n"))
	})
})

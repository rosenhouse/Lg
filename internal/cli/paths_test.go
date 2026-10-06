package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

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
	Entry("for a --pr that is no number", []string{"--pr", "x"}, `--pr`),
)

package e2e_test

import (
	"debug/buildinfo"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg root", Label("cli"), func() {
	var env *harness.Env

	BeforeEach(func() {
		env = harness.New(lgPath)
	})

	It("prints $LG_HOME/data when LG_HOME is set", func() {
		lgHome := GinkgoT().TempDir()
		env.Setenv("LG_HOME", lgHome)
		env.Setenv("XDG_DATA_HOME", GinkgoT().TempDir())

		session := env.Lg("root")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(filepath.Join(lgHome, "data") + "\n"))
	})

	It("prints $XDG_DATA_HOME/lg/data when only XDG_DATA_HOME is set", func() {
		xdgDataHome := GinkgoT().TempDir()
		env.Setenv("XDG_DATA_HOME", xdgDataHome)

		session := env.Lg("root")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(filepath.Join(xdgDataHome, "lg", "data") + "\n"))
	})

	It("prints $HOME/.local/share/lg/data otherwise, on Linux and macOS alike", func() {
		session := env.Lg("root")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(filepath.Join(env.Home(), ".local", "share", "lg", "data") + "\n"))
	})
})

var _ = Describe("lg version", Label("cli"), func() {
	It("prints the version stamped with -ldflags", func() {
		session := harness.New(lgPath).Lg("version")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal("test\n"))
	})

	It("prints the module version that go build stamps without -ldflags", func() {
		path := filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(exec.Command("go", "build", "-o", path, "github.com/rosenhouse/lg/cmd/lg").Run()).To(Succeed())
		info, err := buildinfo.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Main.Version).To(MatchRegexp(`^v\d+\.\d+\.\d+`))

		session := harness.New(path).Lg("version")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(info.Main.Version + "\n"))
	})
})

var _ = Describe("lg", Label("cli"), func() {
	It("exits 2 and prints usage for an unknown command", func() {
		session := harness.New(lgPath).Lg("no-such-command")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say("lg: unexpected argument no-such-command\nUsage: lg <command>"))
	})
})

package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakeservice"
)

var _ = Describe("lg daemon install and uninstall", Label("install"), func() {
	var (
		home, gh       string
		exe, file      string
		env            map[string]string
		runner         *fakeservice.Runner
		stdout, stderr *bytes.Buffer
	)

	BeforeEach(func() {
		home = GinkgoT().TempDir()
		gh = filepath.Join(GinkgoT().TempDir(), "gh")
		Expect(os.WriteFile(gh, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		config := filepath.Join(home, ".config", "lg", "config.yaml")
		Expect(os.MkdirAll(filepath.Dir(config), 0o755)).To(Succeed())
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		env = map[string]string{"HOME": home, "LG_GH": gh}
		runner = &fakeservice.Runner{UnitPath: filepath.Join(home, ".config", "systemd", "user"), Fail: map[string]string{}}
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
		exe, file = "/opt/lg/bin/lg", "/opt/lg/libexec/lg"
	})

	run := func(goos string, args ...string) int {
		return cli.Main(args, cli.Deps{
			Env:        env,
			Stdout:     stdout,
			Stderr:     stderr,
			Clock:      clock.Real{},
			Runner:     runner,
			StoreFS:    store.OSFS{},
			GOOS:       goos,
			Executable: func() (string, string, error) { return exe, file, nil },
		})
	}

	DescribeTable("name the unit and label lg by default, or after the hidden --name",
		func(goos string, args []string, unit string) {
			path := filepath.Join(home, unit)

			Expect(run(goos, append([]string{"daemon", "install"}, args...)...)).To(Equal(0), stderr.String())
			Expect(path).To(BeAnExistingFile())
			Expect(stdout.String()).To(Equal(fmt.Sprintf("installed %s; store %s, config %s\n",
				path, filepath.Join(home, ".local", "share", "lg"), filepath.Join(home, ".config", "lg", "config.yaml"))))

			stdout.Reset()
			Expect(run(goos, append([]string{"daemon", "uninstall"}, args...)...)).To(Equal(0), stderr.String())
			Expect(path).NotTo(BeAnExistingFile())
			Expect(stdout.String()).To(Equal(fmt.Sprintf("removed %s\n", path)))
		},
		Entry(nil, "linux", nil, ".config/systemd/user/lg.service"),
		Entry(nil, "linux", []string{"--name", "lg-test"}, ".config/systemd/user/lg-test.service"),
		Entry(nil, "darwin", nil, "Library/LaunchAgents/com.github.rosenhouse.lg.plist"),
		Entry(nil, "darwin", []string{"--name", "lg-test"}, "Library/LaunchAgents/com.github.rosenhouse.lg-test.plist"),
	)

	DescribeTable("refuses when the path lg was run by, or the file it resolves to, was built by go run",
		func(path, resolved string) {
			exe, file = path, resolved

			Expect(run("linux", "daemon", "install")).To(Equal(1))

			Expect(stderr.String()).To(ContainSubstring("/tmp/go-build1/b001/exe/lg was built by go run"))
			Expect(runner.Calls()).To(BeEmpty())
		},
		Entry(nil, "/tmp/go-build1/b001/exe/lg", "/opt/lg/libexec/lg"),
		Entry(nil, "/opt/lg/bin/lg", "/tmp/go-build1/b001/exe/lg"),
	)

	It("hides --name from help", func() {
		Expect(run("linux", "daemon", "install", "--help")).To(Equal(0))
		Expect(stdout.String()).NotTo(ContainSubstring("--name"))
	})

	It("bakes in lg's executable, the gh it finds and the store's daemon.log", func() {
		Expect(run("darwin", "daemon", "install")).To(Equal(0), stderr.String())

		plist, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.github.rosenhouse.lg.plist"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(plist)).To(And(
			ContainSubstring("<string>/opt/lg/bin/lg</string>"),
			ContainSubstring("<key>LG_GH</key>\n\t\t<string>"+gh+"</string>"),
			ContainSubstring("<string>"+filepath.Join(home, ".local", "share", "lg", "state", "daemon.log")+"</string>"),
		))
	})

	It("bakes in PATH with gh's dir first, and no LG_GH when LG_GH is unset", func() {
		delete(env, "LG_GH")
		env["PATH"] = "/nowhere:" + filepath.Dir(gh)

		Expect(run("darwin", "daemon", "install")).To(Equal(0), stderr.String())

		plist, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.github.rosenhouse.lg.plist"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(plist)).To(ContainSubstring("<key>PATH</key>\n\t\t<string>" + filepath.Dir(gh) + ":/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>"))
		Expect(string(plist)).NotTo(ContainSubstring("LG_GH"))
	})

	It("installs a unit in whose env lg refuses a loopback api_url, so the real gh's token never reaches a local port, when LG_GH is unset", func() {
		delete(env, "LG_GH")
		env["PATH"] = filepath.Dir(gh)
		Expect(run("linux", "daemon", "install")).To(Equal(0), stderr.String())
		config := filepath.Join(home, ".config", "lg", "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\napi_url: http://127.0.0.1:1\n"), 0o644)).To(Succeed())

		env = unitEnv(filepath.Join(home, ".config", "systemd", "user", "lg.service"))
		env["HOME"] = home
		stderr.Reset()

		Expect(run("linux", "sync")).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring("api_url may be on a loopback address only when LG_GH is set"))
	})

	It("bakes in the XDG dirs that chose config.yaml and the store", func() {
		xdg := GinkgoT().TempDir()
		config := filepath.Join(xdg, "config", "lg", "config.yaml")
		Expect(os.MkdirAll(filepath.Dir(config), 0o755)).To(Succeed())
		Expect(os.Rename(filepath.Join(home, ".config", "lg", "config.yaml"), config)).To(Succeed())

		env["XDG_CONFIG_HOME"], env["XDG_DATA_HOME"] = filepath.Join(xdg, "config"), filepath.Join(xdg, "data")

		Expect(run("darwin", "daemon", "install")).To(Equal(0), stderr.String())

		plist, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.github.rosenhouse.lg.plist"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(plist)).To(And(
			ContainSubstring("<key>XDG_CONFIG_HOME</key>\n\t\t<string>"+filepath.Join(xdg, "config")+"</string>"),
			ContainSubstring("<key>XDG_DATA_HOME</key>\n\t\t<string>"+filepath.Join(xdg, "data")+"</string>"),
			ContainSubstring("<string>"+filepath.Join(xdg, "data", "lg", "state", "daemon.log")+"</string>"),
		))
	})

	It("prints installed only when the service starts", func() {
		runner.Fail["enable"] = "Failed to enable unit"

		Expect(run("linux", "daemon", "install")).To(Equal(1))

		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(ContainSubstring("Failed to enable unit"))
	})

	It("says when nothing is installed to uninstall", func() {
		Expect(run("linux", "daemon", "uninstall")).To(Equal(0))
		Expect(stdout.String()).To(Equal("no service named lg is installed\n"))
	})

	It("says when it stopped a service whose unit file was missing", func() {
		runner.Active = true

		Expect(run("linux", "daemon", "uninstall")).To(Equal(0), stderr.String())

		Expect(stdout.String()).To(Equal(fmt.Sprintf("stopped the service named lg, whose unit file %s was missing\n", filepath.Join(home, ".config", "systemd", "user", "lg.service"))))
	})

	It("exits 2 before installing anything when config.yaml is missing", func() {
		Expect(os.Remove(filepath.Join(home, ".config", "lg", "config.yaml"))).To(Succeed())

		Expect(run("linux", "daemon", "install")).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring("config.yaml"))
		Expect(filepath.Join(home, ".config", "systemd")).NotTo(BeADirectory())
		Expect(runner.Calls()).To(BeEmpty())
	})
})

// unitEnv gives the variables a systemd unit sets, whose values hold nothing to unquote.
func unitEnv(unit string) map[string]string {
	GinkgoHelper()
	content, err := os.ReadFile(unit)
	Expect(err).NotTo(HaveOccurred())
	env := map[string]string{}
	for _, line := range strings.Split(string(content), "\n") {
		if kv, ok := strings.CutPrefix(line, `Environment="`); ok {
			k, v, _ := strings.Cut(strings.TrimSuffix(kv, `"`), "=")
			env[k] = v
		}
	}
	return env
}

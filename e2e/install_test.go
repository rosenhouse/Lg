package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakeservice"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

// The golden files name lg and gh by these paths.
const (
	goldenLg = "/opt/lg/bin/lg"
	goldenGh = "/opt/gh/bin/gh"
)

var _ = Describe("lg daemon install on Linux", Label("install"), func() {
	var (
		env       *harness.Env
		systemctl *fakeservice.Fake
		unit      string
	)

	BeforeEach(func() {
		if runtime.GOOS != "linux" {
			Skip("installs a systemd unit only on Linux")
		}
		env, systemctl, unit = newInstallEnv()
	})

	It("writes $XDG_CONFIG_HOME/systemd/user/lg.service (default ~/.config) equal to the golden file, with ExecStart=<abs lg> daemon run, Environment=LG_GH, Restart=on-failure and RestartSec=30", func() {
		golden, err := os.ReadFile("../internal/service/testdata/minimal.service")
		Expect(err).NotTo(HaveOccurred())

		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(goldenPaths(env, unit)).To(Equal(string(golden)))
		Expect(unit).To(Equal(filepath.Join(env.Home(), ".config", "systemd", "user", "lg.service")))

		xdg := filepath.Join(GinkgoT().TempDir(), "xdg")
		env, _, unit = newInstallEnv("XDG_CONFIG_HOME", xdg)
		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(unit).To(Equal(filepath.Join(xdg, "systemd", "user", "lg.service")))
		Expect(goldenPaths(env, unit)).To(Equal(strings.Replace(string(golden), "Restart=", `Environment="XDG_CONFIG_HOME=`+xdg+"\"\nRestart=", 1)))
	})

	It("adds LG_HOME, LG_CONFIG, HTTPS_PROXY, HTTP_PROXY, NO_PROXY and SSL_CERT_FILE only when they are set at install time, and never GH_TOKEN or GITHUB_TOKEN", func() {
		set := map[string]string{
			"LG_HOME":       filepath.Join(GinkgoT().TempDir(), "lg"),
			"LG_CONFIG":     filepath.Join(GinkgoT().TempDir(), "config.yaml"),
			"HTTPS_PROXY":   "http://proxy.example:3128",
			"HTTP_PROXY":    "http://proxy.example:3129",
			"NO_PROXY":      "localhost,127.0.0.1",
			"SSL_CERT_FILE": "/etc/ssl/ca.pem",
		}
		var pairs []string
		for k, v := range set {
			pairs = append(pairs, k, v)
		}
		env, _, unit = newInstallEnv(append(pairs, "GH_TOKEN", "gho_secret1", "GITHUB_TOKEN", "gho_secret2")...)

		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))

		content := readFile(unit)
		for k, v := range set {
			Expect(content).To(ContainSubstring(fmt.Sprintf(`Environment="%s=%s"`+"\n", k, v)))
		}
		Expect(content).NotTo(ContainSubstring("TOKEN"))
		Expect(content).NotTo(ContainSubstring("gho_"))
	})

	It("adds XDG_DATA_HOME and XDG_CONFIG_HOME whenever they are set to an absolute path", func() {
		data, config := filepath.Join(GinkgoT().TempDir(), "data"), filepath.Join(GinkgoT().TempDir(), "config")
		env, _, unit = newInstallEnv("XDG_DATA_HOME", data, "XDG_CONFIG_HOME", config)

		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))

		Expect(readFile(unit)).To(And(
			ContainSubstring(`Environment="XDG_DATA_HOME=`+data+`"`),
			ContainSubstring(`Environment="XDG_CONFIG_HOME=`+config+`"`),
		))
	})

	It("runs `systemctl --user daemon-reload` then `systemctl --user enable --now lg.service`, and on reinstall rewrites the unit and restarts it", func() {
		queries := []string{"--user show -p UnitPath --value", "--user show -p ActiveState --value lg.service"}
		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(systemctl.Calls()).To(Equal(append(queries, "--user daemon-reload", "--user enable --now lg.service")))
		Expect(systemctl.Units()[2:]).To(HaveEach([]string{"lg.service"}))
		fresh := readFile(unit)
		Expect(os.WriteFile(unit, []byte("[Service]\nExecStart=/old/lg daemon run\n"), 0o644)).To(Succeed())

		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))

		Expect(readFile(unit)).To(Equal(fresh))
		Expect(systemctl.Calls()[4:]).To(Equal(append(queries, "--user daemon-reload", "--user enable --now lg.service", "--user restart lg.service")))
	})

	It("runs lg by the symlink it was run by, so an upgrade that repoints the link reaches the unit", func() {
		bin := GinkgoT().TempDir()
		Expect(os.Symlink(lgPath, filepath.Join(bin, "lg"))).To(Succeed())
		env.PrependPath(bin)

		Eventually(env.Sh("lg daemon install"), harness.ExitTimeout).Should(gexec.Exit(0))

		Expect(readFile(unit)).To(ContainSubstring(fmt.Sprintf(`ExecStart="%s" daemon run`, filepath.Join(bin, "lg"))))
	})

	It("produces a unit that systemd-analyze verify accepts", func() {
		analyze, err := exec.LookPath("systemd-analyze")
		if err != nil {
			Skip("systemd-analyze is absent")
		}
		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))

		out, err := exec.CommandContext(GinkgoT().Context(), analyze, "verify", unit).CombinedOutput()

		Expect(err).NotTo(HaveOccurred(), string(out))
		Expect(string(out)).NotTo(ContainSubstring("lg.service"))
	})
})

var _ = Describe("lg daemon install", Label("install"), func() {
	It("refuses when gh is neither on PATH nor at LG_GH", func() {
		env, systemctl, unit := newInstallEnv()
		env.PathWithout("gh")
		env.Setenv("LG_GH", "")

		session := env.Lg("daemon", "install")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(string(session.Err.Contents())).To(ContainSubstring("gh is neither at LG_GH nor on PATH"))
		Expect(unit).NotTo(BeAnExistingFile())
		Expect(systemctl.Calls()).To(BeEmpty())
	})

	It("refuses when lg runs from a `go run` temp dir", func() {
		env, systemctl, unit := newInstallEnv()
		tmp := GinkgoT().TempDir()
		lg := filepath.Join(tmp, "go-build1234", "b001", "exe", "lg")
		Expect(os.MkdirAll(filepath.Dir(lg), 0o755)).To(Succeed())
		Expect(os.WriteFile(lg, readBytes(lgPath), 0o755)).To(Succeed())

		session := env.Sh(fmt.Sprintf("'%s' daemon install", lg))

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(string(session.Err.Contents())).To(ContainSubstring("go run"))
		Expect(unit).NotTo(BeAnExistingFile())
		Expect(systemctl.Calls()).To(BeEmpty())

		bin := GinkgoT().TempDir()
		Expect(os.Symlink(lg, filepath.Join(bin, "lg"))).To(Succeed())
		env.PrependPath(bin)
		session = env.Sh("lg daemon install")

		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(string(session.Err.Contents())).To(ContainSubstring("go run"))
		Expect(unit).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg daemon uninstall", Label("install"), func() {
	It("stops and disables the service before removing the unit or plist", func() {
		env, service, unit := newInstallEnv()
		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))
		verb, stop, file := "disable", "--user disable --now lg.service", "lg.service"
		if runtime.GOOS == "darwin" {
			verb, stop, file = "bootout", fmt.Sprintf("bootout gui/%d/%s", os.Getuid(), launchdLabel), launchdLabel+".plist"
		}

		service.Fail(verb, "Failed to stop")
		Eventually(env.Lg("daemon", "uninstall"), harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(unit).To(BeAnExistingFile())

		service.Unfail(verb)
		before := len(service.Calls())
		Eventually(env.Lg("daemon", "uninstall"), harness.ExitTimeout).Should(gexec.Exit(0))

		Expect(unit).NotTo(BeAnExistingFile())
		calls, units := service.Calls()[before:], service.Units()[before:]
		i := slices.Index(calls, stop)
		Expect(i).To(BeNumerically(">=", 0), "calls: %q", calls)
		Expect(units[i]).To(ContainElement(file))
	})
})

// launchdLabel is the label of the default install on darwin.
const launchdLabel = "com.github.rosenhouse.lg"

var _ = Describe("lg daemon install on darwin", Label("install"), func() {
	It("writes ~/Library/LaunchAgents/<label>.plist that passes plutil -lint, with ThrottleInterval 30 and StandardErrorPath state/daemon.log, and runs launchctl bootstrap gui/<uid>", func() {
		plutil, err := exec.LookPath("plutil")
		if runtime.GOOS != "darwin" || err != nil {
			Skip("needs macOS and plutil")
		}
		env, launchctl, plist := newInstallEnv()
		Expect(plist).To(Equal(filepath.Join(env.Home(), "Library", "LaunchAgents", launchdLabel+".plist")))

		Eventually(env.Lg("daemon", "install"), harness.ExitTimeout).Should(gexec.Exit(0))

		out, err := exec.CommandContext(GinkgoT().Context(), plutil, "-lint", plist).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		out, err = exec.CommandContext(GinkgoT().Context(), plutil, "-convert", "json", "-o", "-", plist).Output()
		Expect(err).NotTo(HaveOccurred())
		var agent map[string]any
		Expect(json.Unmarshal(out, &agent)).To(Succeed())
		Expect(agent).To(HaveKeyWithValue("ThrottleInterval", 30.0))
		Expect(agent).To(HaveKeyWithValue("StandardErrorPath", filepath.Join(env.State(), "daemon.log")))
		Expect(launchctl.Calls()).To(ContainElement(fmt.Sprintf("bootstrap gui/%d %s", os.Getuid(), plist)))
	})
})

var _ = Describe("under a real systemd user manager", Label("systemd"), func() {
	It("runs a daemon installed under a test --name, which syncs from fakegithub and writes status.json", func(ctx SpecContext) {
		home, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred())
		env, _ := newDaemonEnv("backfill: 3650d", "retention: 3650d")
		env.Setenv("LG_HOME", filepath.Join(GinkgoT().TempDir(), "lg"))
		env.Setenv("LG_CONFIG", env.ConfigFile())
		env.Setenv("HOME", home)
		env.Setenv("XDG_RUNTIME_DIR", os.Getenv("XDG_RUNTIME_DIR"))
		name := fmt.Sprintf("lg-test-%d-%d", GinkgoParallelProcess(), GinkgoRandomSeed())

		// Command must be built here: building it in a cleanup would call DeferCleanup there.
		uninstall := env.Command("daemon", "uninstall", "--name", name)
		DeferCleanup(func() {
			if CurrentSpecReport().Failed() {
				journal, _ := exec.CommandContext(context.Background(), "journalctl", "--user", "--no-pager", "-u", name+".service").CombinedOutput()
				AddReportEntry("journal", string(journal))
			}
			out, err := uninstall.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
			Expect(filepath.Join(home, ".config", "systemd", "user", name+".service")).NotTo(BeAnExistingFile())
		})
		Eventually(env.Lg("daemon", "install", "--name", name), harness.ExitTimeout).WithContext(ctx).Should(gexec.Exit(0))

		Eventually(cycle(env), time.Minute).WithContext(ctx).Should(BeNumerically(">=", 1))
		Expect(env.Status()).To(HaveKeyWithValue("last_sync_ok_at", Not(BeNil())))
		Expect(attemptDir(env, 1)).To(BeADirectory())
	}, SpecTimeout(3*time.Minute))
})

// newInstallEnv gives an env with config.yaml written, the further
// variables in kv set, and a fake systemctl or launchctl first on PATH. It
// also gives the path of the default unit or plist.
func newInstallEnv(kv ...string) (*harness.Env, *fakeservice.Fake, string) {
	GinkgoHelper()
	env := harness.New(lgPath)
	for i := 0; i+1 < len(kv); i += 2 {
		env.Setenv(kv[i], kv[i+1])
	}
	config := env.ConfigFile()
	Expect(os.MkdirAll(filepath.Dir(config), 0o755)).To(Succeed())
	Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
	bin := GinkgoT().TempDir()
	env.PrependPath(bin)
	if runtime.GOOS == "darwin" {
		dir := filepath.Join(env.Home(), "Library", "LaunchAgents")
		return env, fakeservice.Launchctl(bin, dir), filepath.Join(dir, launchdLabel+".plist")
	}
	dir := filepath.Join(env.Home(), ".config", "systemd", "user")
	if xdg := env.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dir = filepath.Join(xdg, "systemd", "user")
	}
	return env, fakeservice.Systemctl(bin, dir), filepath.Join(dir, "lg.service")
}

// goldenPaths gives the file at path with env's lg and gh renamed as in the golden files.
func goldenPaths(env *harness.Env, path string) string {
	GinkgoHelper()
	return strings.NewReplacer(lgPath, goldenLg, env.GH().Path, goldenGh).Replace(readFile(path))
}

func readFile(path string) string {
	GinkgoHelper()
	return string(readBytes(path))
}

func readBytes(path string) []byte {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return data
}

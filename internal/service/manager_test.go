package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/service"
	"github.com/rosenhouse/lg/internal/testsupport/fakeservice"
)

var _ = Describe("Manager", Label("install"), func() {
	var (
		home    string
		runner  *fakeservice.Runner
		unit    service.Unit
		systemd string
		agents  string
		uid     = 501
		label   = "com.github.rosenhouse.lg"
	)

	manager := func(goos string) service.Manager {
		return service.Manager{GOOS: goos, Runner: runner, Env: map[string]string{"HOME": home}, UID: uid, Clock: clock.Real{}}
	}

	BeforeEach(func() {
		home = GinkgoT().TempDir()
		systemd = filepath.Join(home, ".config", "systemd", "user")
		agents = filepath.Join(home, "Library", "LaunchAgents")
		runner = &fakeservice.Runner{Fail: map[string]string{}, Out: map[string]string{}, UnitPath: "/etc/systemd/user " + systemd}
		unit = service.Unit{Name: "lg", Exe: "/opt/lg/bin/lg", Env: map[string]string{"LG_GH": "/opt/gh/bin/gh"}, Log: filepath.Join(home, "lg", "state", "daemon.log")}
	})

	Describe("on linux", func() {
		var unitPath, fragmentPath, activeState string

		BeforeEach(func() {
			runner.Dir = systemd
			unitPath = "systemctl --user show -p UnitPath --value"
			fragmentPath = "systemctl --user show -p FragmentPath --value lg.service"
			activeState = "systemctl --user show -p ActiveState --value lg.service"
		})

		It("writes the systemd unit and starts it, running systemctl in the user's env", func() {
			m := manager("linux")
			path, err := m.Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(systemd, "lg.service")))
			want, err := service.RenderSystemd(unit)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(path)).To(Equal(want))
			Expect(runner.Calls()).To(Equal([]string{unitPath, fragmentPath, activeState, "systemctl --user daemon-reload", "systemctl --user enable --now lg.service"}))
			Expect(runner.Files()).To(Equal([][]string{nil, nil, nil, {"lg.service"}, {"lg.service"}}))
			Expect(runner.Envs()).To(HaveEach(Equal(m.Env)))
		})

		It("restarts a running service whose unit it rewrites", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.FragmentPath = path
			unit.Exe = "/opt/lg2/bin/lg"

			Expect(manager("linux").Install(context.Background(), unit)).To(Equal(path))

			Expect(os.ReadFile(path)).To(ContainSubstring("/opt/lg2/bin/lg"))
			Expect(runner.Calls()[5:]).To(Equal([]string{unitPath, fragmentPath, activeState, "systemctl --user daemon-reload", "systemctl --user enable --now lg.service", "systemctl --user restart lg.service"}))
		})

		It("restarts a running service whose unit file is missing", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.FragmentPath = path
			Expect(os.Remove(path)).To(Succeed())
			runner.Reset()

			Expect(manager("linux").Install(context.Background(), unit)).To(Equal(path))

			Expect(runner.Calls()).To(Equal([]string{unitPath, fragmentPath, activeState, "systemctl --user daemon-reload", "systemctl --user enable --now lg.service", "systemctl --user restart lg.service"}))
		})

		It("starts, without restarting, a stopped service whose unit it rewrites", func() {
			_, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Active = false
			runner.Reset()

			_, err = manager("linux").Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.Calls()).To(Equal([]string{unitPath, fragmentPath, activeState, "systemctl --user daemon-reload", "systemctl --user enable --now lg.service"}))
		})

		It("puts the unit under an absolute XDG_CONFIG_HOME, and ignores a relative one", func() {
			xdg := GinkgoT().TempDir()
			runner.UnitPath += " " + filepath.Join(xdg, "systemd", "user")
			m := manager("linux")

			m.Env["XDG_CONFIG_HOME"] = xdg
			Expect(m.Install(context.Background(), unit)).To(Equal(filepath.Join(xdg, "systemd", "user", "lg.service")))
			m.Env["XDG_CONFIG_HOME"] = "rel"
			Expect(m.Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg.service")))
		})

		It("refuses, before writing the unit, a dir the user manager does not load units from", func() {
			runner.UnitPath = "/etc/systemd/user /elsewhere/.config/systemd/user"

			_, err := manager("linux").Install(context.Background(), unit)

			Expect(err).To(MatchError(ContainSubstring("the systemd user manager does not load units from " + systemd)))
			Expect(filepath.Join(systemd, "lg.service")).NotTo(BeAnExistingFile())
			Expect(runner.Calls()).To(Equal([]string{unitPath}))
		})

		It("accepts a dir the user manager loads units from through a symlink", func() {
			Expect(os.MkdirAll(systemd, 0o755)).To(Succeed())
			link := filepath.Join(GinkgoT().TempDir(), "user")
			Expect(os.Symlink(systemd, link)).To(Succeed())
			runner.UnitPath = link

			Expect(manager("linux").Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg.service")))
		})

		It("accepts a dir that UnitPath quotes, as systemd does one holding a space, quote, backslash, dollar or tab", func() {
			home = filepath.Join(home, "sp ace\"q\\b$d\tt")
			systemd = filepath.Join(home, ".config", "systemd", "user")
			quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "\t", `\t`).Replace(systemd) + `"`
			runner.UnitPath = "/etc/systemd/user " + quoted + " /run/systemd/user"

			Expect(manager("linux").Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg.service")))
		})

		DescribeTable("refuses, before writing the unit, when no systemd user manager is reachable, and names `lg daemon run` instead",
			func(setup func()) {
				setup()

				_, err := manager("linux").Install(context.Background(), unit)

				Expect(err).To(MatchError(ContainSubstring("no systemd user manager is reachable")))
				Expect(err).To(MatchError(HaveSuffix("; run `lg daemon run` under your own supervisor instead")))
				Expect(filepath.Join(systemd, "lg.service")).NotTo(BeAnExistingFile())
				Expect(runner.Calls()).To(Equal([]string{unitPath}))
			},
			Entry("when systemctl is missing", func() { runner.Missing = true }),
			Entry("when systemctl cannot reach the user bus", func() { runner.Fail["UnitPath"] = "Failed to connect to bus: No medium found" }),
		)

		It("stops and disables the service, removes the unit, then reloads", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Reset()

			Expect(manager("linux").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Removed: true}))

			Expect(path).NotTo(BeAnExistingFile())
			Expect(runner.Calls()).To(Equal([]string{"systemctl --user disable --now lg.service", "systemctl --user daemon-reload"}))
			Expect(runner.Files()).To(Equal([][]string{{"lg.service"}, nil}))
		})

		It("removes the unit when systemctl is missing, since no user manager can run it", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Missing = true

			Expect(manager("linux").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Removed: true}))

			Expect(path).NotTo(BeAnExistingFile())
		})

		It("stops a running service whose unit file is missing, and removes its dangling wants link", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			wants := filepath.Join(systemd, "default.target.wants", "lg.service")
			Expect(os.MkdirAll(filepath.Dir(wants), 0o755)).To(Succeed())
			Expect(os.Symlink(path, wants)).To(Succeed())
			Expect(os.Remove(path)).To(Succeed())
			runner.FragmentPath = path
			runner.Reset()

			Expect(manager("linux").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Stopped: true}))

			Expect(runner.Calls()).To(Equal([]string{fragmentPath, activeState, "systemctl --user stop lg.service", "systemctl --user daemon-reload"}))
			_, err = os.Lstat(wants)
			Expect(err).To(MatchError(os.ErrNotExist))
		})

		Describe("when the user manager loads the unit from another dir, as one XDG_CONFIG_HOME gave", func() {
			var other string

			BeforeEach(func() {
				other = filepath.Join(GinkgoT().TempDir(), "systemd", "user", "lg.service")
				Expect(os.MkdirAll(filepath.Dir(other), 0o755)).To(Succeed())
				Expect(os.WriteFile(other, nil, 0o644)).To(Succeed())
				runner.FragmentPath, runner.Active = other, true
			})

			It("refuses to install a unit it would not load, before writing it", func() {
				_, err := manager("linux").Install(context.Background(), unit)

				Expect(err).To(MatchError("the systemd user manager loads lg.service from " + other + ", not " + filepath.Join(systemd, "lg.service") + "; give lg the XDG_CONFIG_HOME it was installed with"))
				Expect(filepath.Join(systemd, "lg.service")).NotTo(BeAnExistingFile())
				Expect(runner.Calls()).To(Equal([]string{unitPath, fragmentPath}))
			})

			It("refuses to uninstall, leaving the service running", func() {
				_, err := manager("linux").Uninstall(context.Background(), "lg")

				Expect(err).To(MatchError(ContainSubstring("loads lg.service from " + other)))
				Expect(runner.Calls()).To(Equal([]string{fragmentPath}))
				Expect(runner.Active).To(BeTrue())
			})
		})

		It("keeps the unit when it cannot stop the service, and says why", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Fail["disable"] = "Failed to connect to bus"

			_, err = manager("linux").Uninstall(context.Background(), "lg")

			Expect(err).To(MatchError(And(ContainSubstring("systemctl --user disable --now lg.service"), ContainSubstring("Failed to connect to bus"))))
			Expect(path).To(BeAnExistingFile())
		})

		DescribeTable("when it cannot disable the service, removes the unit only if systemd says the service is not running",
			func(state string, removed bool) {
				path, err := manager("linux").Install(context.Background(), unit)
				Expect(err).NotTo(HaveOccurred())
				runner.Fail["disable"] = "Unit file lg.service does not exist"
				if state == "" {
					runner.Fail["ActiveState"] = "Failed to connect to bus"
				} else {
					runner.Out["ActiveState"] = state
				}
				runner.Reset()

				_, err = manager("linux").Uninstall(context.Background(), "lg")

				Expect(runner.Calls()[1]).To(Equal(activeState))
				if removed {
					Expect(err).NotTo(HaveOccurred())
					Expect(path).NotTo(BeAnExistingFile())
					Expect(runner.Calls()[2:]).To(Equal([]string{"systemctl --user daemon-reload"}))
				} else {
					Expect(err).To(MatchError(ContainSubstring("Unit file lg.service does not exist")))
					Expect(path).To(BeAnExistingFile())
				}
			},
			Entry(nil, "inactive", true),
			Entry(nil, "failed", true),
			Entry(nil, "active", false),
			Entry(nil, "activating", false),
			Entry("when systemctl show fails", "", false),
		)

		It("says why systemctl failed to start the unit, which it leaves in place", func() {
			runner.Fail["enable"] = "Failed to enable unit"

			_, err := manager("linux").Install(context.Background(), unit)

			Expect(err).To(MatchError(And(ContainSubstring("systemctl --user enable --now lg.service"), ContainSubstring("Failed to enable unit"))))
			Expect(filepath.Join(systemd, "lg.service")).To(BeAnExistingFile())
		})
	})

	Describe("on darwin", func() {
		var print, bootout string

		BeforeEach(func() {
			runner.Dir = agents
			print, bootout = fmt.Sprintf("launchctl print gui/%d/%s", uid, label), fmt.Sprintf("launchctl bootout gui/%d/%s", uid, label)
		})

		It("writes the plist, creates the log's dir, and bootstraps it, running launchctl in the user's env", func() {
			m := manager("darwin")
			path, err := m.Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(agents, label+".plist")))
			want, err := service.RenderLaunchd(unit)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(path)).To(Equal(want))
			Expect(filepath.Dir(unit.Log)).To(BeADirectory())
			Expect(runner.Calls()).To(Equal([]string{print, fmt.Sprintf("launchctl bootstrap gui/%d %s", uid, path)}))
			Expect(runner.Envs()).To(HaveEach(Equal(m.Env)))
		})

		It("boots out a loaded agent, and waits for it to unload, before it rewrites and bootstraps it", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Reset()
			runner.Lingering = 1
			unit.Exe = "/opt/lg2/bin/lg"

			Expect(manager("darwin").Install(context.Background(), unit)).To(Equal(path))

			Expect(runner.Calls()).To(Equal([]string{print, bootout, print, print, fmt.Sprintf("launchctl bootstrap gui/%d %s", uid, path)}))
			Expect(runner.Files()[1]).To(ConsistOf(label+".plist", MatchRegexp(`^\.`+regexp.QuoteMeta(label)+`\.plist\.\d+\.tmp$`)), "launchd and systemd skip a dotfile")
			Expect(runner.Files()[4]).To(Equal([]string{label + ".plist"}))
			Expect(os.ReadFile(path)).To(ContainSubstring("/opt/lg2/bin/lg"))
		})

		DescribeTable("keeps the old plist, and says so, when a booted-out agent does not unload within 10s",
			func(setup func(), waiters int) {
				path, err := manager("darwin").Install(context.Background(), unit)
				Expect(err).NotTo(HaveOccurred())
				old, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred())
				setup()
				unit.Exe = "/opt/lg2/bin/lg"
				start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
				clk := clock.NewFake(start)
				m := manager("darwin")
				m.Clock = clk
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)
				errs := make(chan error, 1)

				go func() {
					_, err := m.Install(ctx, unit)
					errs <- err
				}()
				Eventually(clk.Waiting).Should(Equal(waiters))
				clk.Set(start.Add(10*time.Second - time.Nanosecond))
				Consistently(errs).ShouldNot(Receive())
				clk.Set(start.Add(10 * time.Second))

				Eventually(errs).Should(Receive(MatchError(ContainSubstring("still loaded"))))
				Expect(os.ReadFile(path)).To(Equal(old))
				Expect(os.ReadDir(agents)).To(HaveLen(1))
			},
			Entry("when print keeps finding it", func() { runner.Lingering = 1 << 30 }, 2),
			Entry("when print hangs until it is killed", func() { runner.Hang = true }, 1),
		)

		It("leaves a loaded agent alone when it cannot create the log's dir", func() {
			_, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			blocker := filepath.Join(home, "blocker")
			Expect(os.WriteFile(blocker, nil, 0o644)).To(Succeed())
			unit.Log = filepath.Join(blocker, "state", "daemon.log")
			runner.Reset()

			_, err = manager("darwin").Install(context.Background(), unit)

			Expect(err).To(MatchError(ContainSubstring("not a directory")))
			Expect(runner.Calls()).To(BeEmpty())
			Expect(runner.Loaded).To(BeTrue())
		})

		It("says the agent is not loaded when bootstrap fails", func() {
			runner.Fail["bootstrap"] = "Bootstrap failed: 5: Input/output error"

			_, err := manager("darwin").Install(context.Background(), unit)

			Expect(err).To(MatchError(And(ContainSubstring("Input/output error"), ContainSubstring("the agent is not loaded"))))
		})

		It("boots out a loaded agent before it removes the plist", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Reset()

			Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Removed: true}))

			Expect(path).NotTo(BeAnExistingFile())
			i := slices.Index(runner.Calls(), bootout)
			Expect(i).To(BeNumerically(">=", 0))
			Expect(runner.Files()[i]).To(ContainElement(label + ".plist"))
		})

		It("removes the plist of an agent that is not loaded", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.Loaded = false
			runner.Reset()

			Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Removed: true}))

			Expect(path).NotTo(BeAnExistingFile())
			Expect(runner.Calls()).To(Equal([]string{print}))
		})

		It("boots out a loaded agent whose plist is missing", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Remove(path)).To(Succeed())

			Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: path, Stopped: true}))

			Expect(runner.Calls()).To(ContainElement(bootout))
			Expect(runner.Loaded).To(BeFalse())
		})
	})

	DescribeTable("picks its backend from GOOS",
		func(goos, dir, file, command string) {
			path, err := manager(goos).Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(home, dir, file)))
			Expect(runner.Calls()).To(HaveEach(HavePrefix(command + " ")))
		},
		Entry(nil, "linux", ".config/systemd/user", "lg.service", "systemctl"),
		Entry(nil, "darwin", "Library/LaunchAgents", "com.github.rosenhouse.lg.plist", "launchctl"),
	)

	It("refuses another GOOS before it writes or runs anything", func() {
		_, err := manager("windows").Install(context.Background(), unit)

		Expect(err).To(MatchError(ContainSubstring("linux and darwin")))
		Expect(runner.Calls()).To(BeEmpty())
		Expect(os.ReadDir(home)).To(BeEmpty())
	})

	It("names the unit and label after Unit.Name", func() {
		unit.Name = "lg-test-1"

		Expect(manager("linux").Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg-test-1.service")))
		Expect(runner.Calls()).To(ContainElement("systemctl --user enable --now lg-test-1.service"))
		Expect(manager("darwin").Install(context.Background(), unit)).To(Equal(filepath.Join(agents, "com.github.rosenhouse.lg-test-1.plist")))
	})

	DescribeTable("refuses a name that is not a plain file name",
		func(name string) {
			unit.Name = name
			_, installErr := manager("linux").Install(context.Background(), unit)
			_, uninstallErr := manager("darwin").Uninstall(context.Background(), name)

			Expect(installErr).To(MatchError(ContainSubstring("invalid service name")))
			Expect(uninstallErr).To(MatchError(ContainSubstring("invalid service name")))
			Expect(runner.Calls()).To(BeEmpty())
		},
		Entry(nil, ""),
		Entry(nil, "../lg"),
		Entry(nil, "a/b"),
		Entry(nil, ".lg"),
		Entry(nil, "-lg"),
		Entry(nil, "lg service"),
	)

	DescribeTable("does nothing to uninstall when nothing is installed or running",
		func(goos, file string, missing bool, queries int) {
			runner.Missing = missing

			Expect(manager(goos).Uninstall(context.Background(), "lg")).To(Equal(service.Removal{Path: filepath.Join(home, file)}))

			Expect(runner.Calls()).To(HaveLen(queries))
		},
		Entry(nil, "linux", ".config/systemd/user/lg.service", false, 2),
		Entry("when systemctl is missing", "linux", ".config/systemd/user/lg.service", true, 2),
		Entry(nil, "darwin", "Library/LaunchAgents/com.github.rosenhouse.lg.plist", false, 1),
	)

	DescribeTable("waits for another install or uninstall in the unit's dir to finish",
		func(goos string, dir func() string, uninstall bool) {
			Expect(os.MkdirAll(dir(), 0o755)).To(Succeed())
			f, err := os.Open(dir())
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(f.Close)
			Expect(syscall.Flock(int(f.Fd()), syscall.LOCK_EX)).To(Succeed())
			errs := make(chan error, 1)

			go func() {
				var err error
				if uninstall {
					_, err = manager(goos).Uninstall(context.Background(), "lg")
				} else {
					_, err = manager(goos).Install(context.Background(), unit)
				}
				errs <- err
			}()

			Consistently(runner.Calls).Should(BeEmpty())
			Expect(syscall.Flock(int(f.Fd()), syscall.LOCK_UN)).To(Succeed())
			Eventually(errs).Should(Receive(Succeed()))
			Expect(runner.Calls()).NotTo(BeEmpty())
		},
		Entry("install on linux", "linux", func() string { return systemd }, false),
		Entry("uninstall on linux", "linux", func() string { return systemd }, true),
		Entry("install on darwin", "darwin", func() string { return agents }, false),
		Entry("uninstall on darwin", "darwin", func() string { return agents }, true),
	)

	It("removes a temp unit file left by an interrupted install", func() {
		Expect(os.MkdirAll(systemd, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(systemd, ".lg.service.123.tmp"), nil, 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(systemd, ".lg-test.service.123.tmp"), nil, 0o600)).To(Succeed())

		Expect(manager("linux").Install(context.Background(), unit)).Error().NotTo(HaveOccurred())

		Expect(os.ReadDir(systemd)).To(HaveLen(2))
		Expect(filepath.Join(systemd, ".lg-test.service.123.tmp")).To(BeAnExistingFile())
	})

	It("installs the same unit from concurrent calls", func() {
		m := service.Manager{GOOS: "linux", Runner: &fakeservice.Runner{UnitPath: systemd}, Env: map[string]string{"HOME": home}}
		errs := make(chan error, 8)
		for range cap(errs) {
			go func() {
				_, err := m.Install(context.Background(), unit)
				errs <- err
			}()
		}
		for range cap(errs) {
			Expect(<-errs).NotTo(HaveOccurred())
		}
		Expect(os.ReadDir(systemd)).To(HaveLen(1))
	})

	It("leaves the unit readable only by its owner, since it may hold proxy credentials", func() {
		path := filepath.Join(systemd, "lg.service")
		Expect(os.MkdirAll(systemd, 0o755)).To(Succeed())
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())

		Expect(manager("linux").Install(context.Background(), unit)).To(Equal(path))

		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("refuses a relative HOME", func() {
		m := manager("linux")
		m.Env["HOME"] = "home"

		_, err := m.Install(context.Background(), unit)

		Expect(err).To(MatchError(`HOME must be an absolute path: "home"`))
		Expect(runner.Calls()).To(BeEmpty())
	})
})

var _ = DescribeTable("SplitQuoted splits a list as systemctl show prints one", Label("install"),
	func(list string, words []string) {
		Expect(service.SplitQuoted(list)).To(Equal(words))
	},
	Entry("bare words", "/a  /b\n", []string{"/a", "/b"}),
	Entry("a quoted space", `"/s p" /b`, []string{"/s p", "/b"}),
	Entry("shell escapes", `"/q\"b\\s\$d\`+"`e\"", []string{"/q\"b\\s$d`e"}),
	Entry("C escapes", `"\a\b\f\n\r\t\v\001\177"`, []string{"\a\b\f\n\r\t\v\001\177"}),
	Entry("a backslash ending the list", `"/a\`, []string{`/a\`}),
	Entry("an empty quoted word", `"" /b`, []string{"", "/b"}),
	Entry("nothing", "\n", nil),
)

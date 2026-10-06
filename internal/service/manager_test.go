package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/service"
)

// fakeRunner logs each call, and the files then in dir. A call whose
// arguments include a key of fail prints its value and fails; one whose
// arguments include a key of out prints its value to stdout. It models
// launchctl's one service: bootstrap loads it, bootout unloads it, and
// print fails while it is not loaded.
type fakeRunner struct {
	dir    string
	calls  []string
	files  [][]string
	fail   map[string]string
	out    map[string]string
	loaded bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	entries, _ := os.ReadDir(f.dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	f.files = append(f.files, names)
	for _, a := range args {
		if msg, ok := f.fail[a]; ok {
			return nil, []byte(msg + "\n"), errors.New("exit status 1")
		}
	}
	for _, a := range args {
		if out, ok := f.out[a]; ok {
			return []byte(out + "\n"), nil, nil
		}
	}
	if name == "launchctl" {
		switch args[0] {
		case "bootstrap":
			f.loaded = true
		case "bootout":
			f.loaded = false
		case "print":
			if !f.loaded {
				return nil, []byte("Could not find service\n"), errors.New("exit status 113")
			}
		}
	}
	return nil, nil, nil
}

var _ = Describe("Manager", Label("install"), func() {
	var (
		home    string
		runner  *fakeRunner
		unit    service.Unit
		systemd string
		agents  string
		uid     = 501
		label   = "com.github.rosenhouse.lg"
	)

	manager := func(goos string) service.Manager {
		return service.Manager{GOOS: goos, Runner: runner, Env: map[string]string{"HOME": home}, UID: uid}
	}

	BeforeEach(func() {
		home = GinkgoT().TempDir()
		systemd = filepath.Join(home, ".config", "systemd", "user")
		agents = filepath.Join(home, "Library", "LaunchAgents")
		runner = &fakeRunner{fail: map[string]string{}, out: map[string]string{}}
		unit = service.Unit{Name: "lg", Exe: "/opt/lg/bin/lg", Env: map[string]string{"LG_GH": "/opt/gh/bin/gh"}, Log: filepath.Join(home, "lg", "state", "daemon.log")}
	})

	Describe("on linux", func() {
		BeforeEach(func() { runner.dir = systemd })

		It("writes the systemd unit and starts it", func() {
			path, err := manager("linux").Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(systemd, "lg.service")))
			want, err := service.RenderSystemd(unit)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(path)).To(Equal(want))
			Expect(runner.calls).To(Equal([]string{"systemctl --user daemon-reload", "systemctl --user enable --now lg.service"}))
			Expect(runner.files).To(HaveEach([]string{"lg.service"}))
		})

		It("restarts a unit it rewrites", func() {
			_, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			unit.Exe = "/opt/lg2/bin/lg"

			path, err := manager("linux").Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(path)).To(ContainSubstring("/opt/lg2/bin/lg"))
			Expect(runner.calls[2:]).To(Equal([]string{"systemctl --user daemon-reload", "systemctl --user enable --now lg.service", "systemctl --user restart lg.service"}))
		})

		It("puts the unit under an absolute XDG_CONFIG_HOME, and ignores a relative one", func() {
			xdg := GinkgoT().TempDir()
			m := manager("linux")

			m.Env["XDG_CONFIG_HOME"] = xdg
			Expect(m.Install(context.Background(), unit)).To(Equal(filepath.Join(xdg, "systemd", "user", "lg.service")))
			m.Env["XDG_CONFIG_HOME"] = "rel"
			Expect(m.Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg.service")))
		})

		It("stops and disables the service, removes the unit, then reloads", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.calls, runner.files = nil, nil

			Expect(manager("linux").Uninstall(context.Background(), "lg")).To(Equal(path))

			Expect(path).NotTo(BeAnExistingFile())
			Expect(runner.calls).To(Equal([]string{"systemctl --user disable --now lg.service", "systemctl --user daemon-reload"}))
			Expect(runner.files).To(Equal([][]string{{"lg.service"}, nil}))
		})

		It("keeps the unit when it cannot stop the service, and says why", func() {
			path, err := manager("linux").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.fail["disable"] = "Failed to connect to bus"

			_, err = manager("linux").Uninstall(context.Background(), "lg")

			Expect(err).To(MatchError(And(ContainSubstring("systemctl --user disable --now lg.service"), ContainSubstring("Failed to connect to bus"))))
			Expect(path).To(BeAnExistingFile())
		})

		DescribeTable("when it cannot disable the service, removes the unit only if systemd says the service is not running",
			func(state string, removed bool) {
				path, err := manager("linux").Install(context.Background(), unit)
				Expect(err).NotTo(HaveOccurred())
				runner.fail["disable"] = "Unit file lg.service does not exist"
				if state == "" {
					runner.fail["show"] = "Failed to connect to bus"
				} else {
					runner.out["show"] = state
				}
				runner.calls = nil

				_, err = manager("linux").Uninstall(context.Background(), "lg")

				Expect(runner.calls[1]).To(Equal("systemctl --user show -p ActiveState --value lg.service"))
				if removed {
					Expect(err).NotTo(HaveOccurred())
					Expect(path).NotTo(BeAnExistingFile())
					Expect(runner.calls[2:]).To(Equal([]string{"systemctl --user daemon-reload"}))
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
			runner.fail["enable"] = "Failed to connect to bus: No medium found"

			_, err := manager("linux").Install(context.Background(), unit)

			Expect(err).To(MatchError(And(ContainSubstring("systemctl --user enable --now lg.service"), ContainSubstring("No medium found"))))
			Expect(filepath.Join(systemd, "lg.service")).To(BeAnExistingFile())
		})
	})

	Describe("on darwin", func() {
		BeforeEach(func() { runner.dir = agents })

		It("writes the plist, creates the log's dir, and bootstraps it", func() {
			path, err := manager("darwin").Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(agents, label+".plist")))
			want, err := service.RenderLaunchd(unit)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(path)).To(Equal(want))
			Expect(filepath.Dir(unit.Log)).To(BeADirectory())
			Expect(runner.calls).To(Equal([]string{
				fmt.Sprintf("launchctl print gui/%d/%s", uid, label),
				fmt.Sprintf("launchctl bootstrap gui/%d %s", uid, path),
			}))
		})

		It("boots out a loaded agent before it rewrites and bootstraps it", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.calls = nil

			Expect(manager("darwin").Install(context.Background(), unit)).To(Equal(path))

			Expect(runner.calls).To(Equal([]string{
				fmt.Sprintf("launchctl print gui/%d/%s", uid, label),
				fmt.Sprintf("launchctl bootout gui/%d/%s", uid, label),
				fmt.Sprintf("launchctl bootstrap gui/%d %s", uid, path),
			}))
		})

		It("boots out a loaded agent before it removes the plist", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.calls, runner.files = nil, nil

			Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(Equal(path))

			Expect(path).NotTo(BeAnExistingFile())
			bootout := slices.Index(runner.calls, fmt.Sprintf("launchctl bootout gui/%d/%s", uid, label))
			Expect(bootout).To(BeNumerically(">=", 0))
			Expect(runner.files[bootout]).To(ContainElement(label + ".plist"))
		})

		It("removes the plist of an agent that is not loaded", func() {
			path, err := manager("darwin").Install(context.Background(), unit)
			Expect(err).NotTo(HaveOccurred())
			runner.loaded = false
			runner.calls = nil

			Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(Equal(path))

			Expect(path).NotTo(BeAnExistingFile())
			Expect(runner.calls).To(Equal([]string{fmt.Sprintf("launchctl print gui/%d/%s", uid, label)}))
		})
	})

	DescribeTable("picks its backend from GOOS",
		func(goos, dir, file, command string) {
			path, err := manager(goos).Install(context.Background(), unit)

			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(home, dir, file)))
			Expect(runner.calls).To(HaveEach(HavePrefix(command + " ")))
		},
		Entry(nil, "linux", ".config/systemd/user", "lg.service", "systemctl"),
		Entry(nil, "darwin", "Library/LaunchAgents", "com.github.rosenhouse.lg.plist", "launchctl"),
	)

	It("refuses another GOOS before it writes or runs anything", func() {
		_, err := manager("windows").Install(context.Background(), unit)

		Expect(err).To(MatchError(ContainSubstring("linux and darwin")))
		Expect(runner.calls).To(BeEmpty())
		Expect(os.ReadDir(home)).To(BeEmpty())
	})

	It("names the unit and label after Unit.Name", func() {
		unit.Name = "lg-test-1"

		Expect(manager("linux").Install(context.Background(), unit)).To(Equal(filepath.Join(systemd, "lg-test-1.service")))
		Expect(runner.calls).To(ContainElement("systemctl --user enable --now lg-test-1.service"))
		Expect(manager("darwin").Install(context.Background(), unit)).To(Equal(filepath.Join(agents, "com.github.rosenhouse.lg-test-1.plist")))
	})

	DescribeTable("refuses a name that is not a plain file name",
		func(name string) {
			unit.Name = name
			_, installErr := manager("linux").Install(context.Background(), unit)
			_, uninstallErr := manager("darwin").Uninstall(context.Background(), name)

			Expect(installErr).To(MatchError(ContainSubstring("invalid service name")))
			Expect(uninstallErr).To(MatchError(ContainSubstring("invalid service name")))
			Expect(runner.calls).To(BeEmpty())
		},
		Entry(nil, ""),
		Entry(nil, "../lg"),
		Entry(nil, "a/b"),
		Entry(nil, ".lg"),
		Entry(nil, "-lg"),
		Entry(nil, "lg service"),
	)

	It("does nothing to uninstall when nothing is installed", func() {
		Expect(manager("linux").Uninstall(context.Background(), "lg")).To(BeEmpty())
		Expect(manager("darwin").Uninstall(context.Background(), "lg")).To(BeEmpty())
		Expect(runner.calls).To(BeEmpty())
	})

	It("refuses a relative HOME", func() {
		m := manager("linux")
		m.Env["HOME"] = "home"

		_, err := m.Install(context.Background(), unit)

		Expect(err).To(MatchError(ContainSubstring("HOME")))
	})
})

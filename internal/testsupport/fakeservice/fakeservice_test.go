package fakeservice_test

import (
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakeservice"
)

var _ = Describe("fake systemctl", Label("install"), func() {
	var (
		bin, units string
		fake       *fakeservice.Fake
	)

	run := func(args ...string) (string, error) {
		out, err := exec.CommandContext(GinkgoT().Context(), filepath.Join(bin, "systemctl"), args...).CombinedOutput()
		return string(out), err
	}

	BeforeEach(func() {
		bin, units = GinkgoT().TempDir(), GinkgoT().TempDir()
		fake = fakeservice.Systemctl(bin, units)
	})

	It("logs each run's arguments and the files then in the unit dir", func() {
		Expect(run("--user", "daemon-reload")).To(BeEmpty())
		Expect(os.WriteFile(filepath.Join(units, "lg.service"), nil, 0o644)).To(Succeed())
		Expect(run("--user", "enable", "--now", "lg.service")).To(BeEmpty())

		Expect(fake.Calls()).To(Equal([]string{"--user daemon-reload", "--user enable --now lg.service"}))
		Expect(fake.Units()).To(Equal([][]string{nil, {"lg.service"}}))
	})

	It("loads units from the unit dir, and starts and stops one service", func() {
		Expect(run("--user", "show", "-p", "UnitPath", "--value")).To(Equal(units + "\n"))
		Expect(run("--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("inactive\n"))
		Expect(run("--user", "enable", "--now", "lg.service")).To(BeEmpty())
		Expect(run("--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("active\n"))
		Expect(run("--user", "disable", "--now", "lg.service")).To(BeEmpty())
		Expect(run("--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("inactive\n"))
		Expect(run("--user", "restart", "lg.service")).To(BeEmpty())
		Expect(run("--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("active\n"))
		Expect(run("--user", "stop", "lg.service")).To(BeEmpty())
		Expect(run("--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("inactive\n"))
	})

	It("fails a run with an argument Fail names until Unfail", func() {
		fake.Fail("disable", "Failed to disable unit")

		out, err := run("--user", "disable", "lg.service")
		Expect(err).To(HaveOccurred())
		Expect(out).To(Equal("Failed to disable unit\n"))
		Expect(run("--user", "daemon-reload")).To(BeEmpty())

		fake.Unfail("disable")
		Expect(run("--user", "disable", "lg.service")).To(BeEmpty())
		Expect(fake.Calls()).To(HaveLen(3))
	})
})

var _ = Describe("fake launchctl", Label("install"), func() {
	It("prints a service only while it is bootstrapped", func() {
		bin := GinkgoT().TempDir()
		fakeservice.Launchctl(bin, GinkgoT().TempDir())
		launchctl := func(args ...string) error {
			return exec.CommandContext(GinkgoT().Context(), filepath.Join(bin, "launchctl"), args...).Run()
		}

		Expect(launchctl("print", "gui/501/l")).NotTo(Succeed())
		Expect(launchctl("bootout", "gui/501/l")).NotTo(Succeed())
		Expect(launchctl("bootstrap", "gui/501", "/l.plist")).To(Succeed())
		Expect(launchctl("print", "gui/501/l")).To(Succeed())
		Expect(launchctl("bootout", "gui/501/l")).To(Succeed())
		Expect(launchctl("print", "gui/501/l")).NotTo(Succeed())
	})
})

var _ = Describe("Runner", Label("install"), func() {
	var r *fakeservice.Runner

	run := func(name string, args ...string) (string, error) {
		stdout, stderr, err := r.Run(GinkgoT().Context(), name, args, map[string]string{"HOME": "/h"})
		return string(stdout) + string(stderr), err
	}

	BeforeEach(func() {
		r = &fakeservice.Runner{Dir: GinkgoT().TempDir(), UnitPath: "/u", Fail: map[string]string{}, Out: map[string]string{}}
	})

	It("logs each call, the files then in Dir, and its env", func() {
		Expect(run("systemctl", "--user", "daemon-reload")).To(BeEmpty())
		Expect(os.WriteFile(filepath.Join(r.Dir, "lg.service"), nil, 0o644)).To(Succeed())
		Expect(run("launchctl", "bootstrap", "gui/501", "/l.plist")).To(BeEmpty())

		Expect(r.Calls()).To(Equal([]string{"systemctl --user daemon-reload", "launchctl bootstrap gui/501 /l.plist"}))
		Expect(r.Files()).To(Equal([][]string{nil, {"lg.service"}}))
		Expect(r.Envs()).To(HaveEach(Equal(map[string]string{"HOME": "/h"})))
		r.Reset()
		Expect(r.Calls()).To(BeEmpty())
	})

	It("loads units from UnitPath, and the service from FragmentPath", func() {
		r.FragmentPath = "/u/lg.service"

		Expect(run("systemctl", "--user", "show", "-p", "UnitPath", "--value")).To(Equal("/u\n"))
		Expect(run("systemctl", "--user", "show", "-p", "FragmentPath", "--value", "lg.service")).To(Equal("/u/lg.service\n"))
	})

	It("starts and stops one service", func() {
		state := func() string {
			out, err := run("systemctl", "--user", "show", "-p", "ActiveState", "--value", "lg.service")
			Expect(err).NotTo(HaveOccurred())
			return out
		}
		Expect(state()).To(Equal("inactive\n"))
		Expect(run("systemctl", "--user", "enable", "lg.service")).To(BeEmpty())
		Expect(state()).To(Equal("inactive\n"))
		Expect(run("systemctl", "--user", "enable", "--now", "lg.service")).To(BeEmpty())
		Expect(state()).To(Equal("active\n"))
		Expect(run("systemctl", "--user", "disable", "--now", "lg.service")).To(BeEmpty())
		Expect(state()).To(Equal("inactive\n"))
		Expect(run("systemctl", "--user", "restart", "lg.service")).To(BeEmpty())
		Expect(state()).To(Equal("active\n"))
		Expect(run("systemctl", "--user", "stop", "lg.service")).To(BeEmpty())
		Expect(state()).To(Equal("inactive\n"))
	})

	It("prints an agent while it is bootstrapped, and for Lingering prints after bootout", func() {
		r.Lingering = 1
		print := func() error {
			_, err := run("launchctl", "print", "gui/501/l")
			return err
		}

		Expect(print()).NotTo(Succeed())
		Expect(run("launchctl", "bootstrap", "gui/501", "/l.plist")).To(BeEmpty())
		Expect(print()).To(Succeed())
		Expect(run("launchctl", "bootout", "gui/501/l")).To(BeEmpty())
		Expect(print()).To(Succeed())
		Expect(print()).NotTo(Succeed())
	})

	It("fails a call with an argument Fail names, prints what Out names, and finds no command when Missing", func() {
		r.Fail["disable"] = "Failed to disable unit"
		r.Out["ActiveState"] = "failed"

		out, err := run("systemctl", "--user", "disable", "lg.service")
		Expect(err).To(HaveOccurred())
		Expect(out).To(Equal("Failed to disable unit\n"))
		Expect(run("systemctl", "--user", "show", "-p", "ActiveState", "--value", "lg.service")).To(Equal("failed\n"))

		r.Missing = true
		_, err = run("systemctl", "--user", "daemon-reload")
		Expect(err).To(MatchError(exec.ErrNotFound))
	})
})

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

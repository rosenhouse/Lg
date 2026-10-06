// Package fakeservice writes fake systemctl and launchctl scripts for specs.
package fakeservice

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type Fake struct {
	dir string
}

// Systemctl writes a systemctl into binDir. Each run appends its arguments
// to Calls and the files then in unitDir to Units. A run with an argument
// that Fail named prints that stderr and exits 1. It models a user manager
// that loads units from unitDir, and one service, which enable --now and
// restart start, and disable --now and stop stop.
func Systemctl(binDir, unitDir string) *Fake {
	ginkgo.GinkgoHelper()
	return write(binDir, "systemctl", unitDir, func(f *Fake) string {
		return fmt.Sprintf(`case "$2 $3 $4" in
"show -p UnitPath") echo '%[1]s' ;;
"show -p ActiveState") if [ -e '%[2]s' ]; then echo active; else echo inactive; fi ;;
"enable --now "*|restart*) : > '%[2]s' ;;
"disable --now "*|stop*) rm -f '%[2]s' ;;
esac
`, unitDir, f.file("active"))
	})
}

// Launchctl writes a launchctl into binDir that logs and fails as Systemctl's
// does. It models one service, which bootstrap loads and bootout unloads, and
// print fails while it is not loaded.
func Launchctl(binDir, unitDir string) *Fake {
	ginkgo.GinkgoHelper()
	return write(binDir, "launchctl", unitDir, func(f *Fake) string {
		return fmt.Sprintf(`case "$1" in
bootstrap) : > '%[1]s' ;;
bootout) [ -e '%[1]s' ] || { echo 'Boot-out failed: 3: No such process' >&2; exit 3; }; rm '%[1]s' ;;
print) [ -e '%[1]s' ] || { echo 'Could not find service' >&2; exit 113; } ;;
esac
`, f.file("loaded"))
	})
}

func write(binDir, name, unitDir string, model func(*Fake) string) *Fake {
	ginkgo.GinkgoHelper()
	f := &Fake{dir: ginkgo.GinkgoT().TempDir()}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
printf '%%s\n' "$(ls -A '%s' 2>/dev/null | tr '\n' ' ')" >> '%s'
for a in "$@"; do
	if [ -e "%s/fail-$a" ]; then cat "%[4]s/fail-$a" >&2; exit 1; fi
done
`, f.file("calls"), unitDir, f.file("units"), f.dir) + model(f)
	gomega.Expect(os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755)).To(gomega.Succeed())
	return f
}

// Fail makes each later run with arg among its arguments print stderr and exit 1.
func (f *Fake) Fail(arg, stderr string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.WriteFile(f.file("fail-"+arg), []byte(stderr+"\n"), 0o644)).To(gomega.Succeed())
}

// Unfail undoes Fail(arg).
func (f *Fake) Unfail(arg string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.Remove(f.file("fail-" + arg))).To(gomega.Succeed())
}

// Calls gives the arguments of each run, joined by spaces.
func (f *Fake) Calls() []string {
	ginkgo.GinkgoHelper()
	return f.lines("calls")
}

// Units gives, for each run, the names of the files in its unit dir.
func (f *Fake) Units() [][]string {
	ginkgo.GinkgoHelper()
	var units [][]string
	for _, line := range f.lines("units") {
		var names []string
		units = append(units, append(names, strings.Fields(line)...))
	}
	return units
}

func (f *Fake) lines(name string) []string {
	ginkgo.GinkgoHelper()
	data, err := os.ReadFile(f.file(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (f *Fake) file(name string) string { return filepath.Join(f.dir, name) }

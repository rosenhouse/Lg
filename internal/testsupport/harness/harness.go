// Package harness runs the built lg binary in a scrubbed, per-spec environment.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// ExitTimeout bounds how long a spec waits for a short-lived lg command.
const ExitTimeout = 10 * time.Second

type Env struct {
	lgPath string
	vars   map[string]string
	gh     *fakegh.GH
}

// New gives the calling spec its own HOME, a fake gh at LG_GH, LG_TEST_NOW
// at DefaultNow and an environment built from os.Environ by Scrub.
func New(lgPath string) *Env {
	vars := Scrub(os.Environ(), filepath.Dir(lgPath))
	vars["HOME"] = ginkgo.GinkgoT().TempDir()
	vars["LG_TEST_NOW"] = DefaultNow.Format(time.RFC3339)
	gh := fakegh.New(ginkgo.GinkgoT().TempDir())
	vars["LG_GH"] = gh.Path
	return &Env{lgPath: lgPath, vars: vars, gh: gh}
}

func (e *Env) Home() string { return e.vars["HOME"] }

func (e *Env) GH() *fakegh.GH { return e.gh }

func (e *Env) Setenv(key, value string) { e.vars[key] = value }

func (e *Env) Lg(args ...string) *gexec.Session {
	return e.start(exec.CommandContext(ginkgo.GinkgoT().Context(), e.lgPath, args...))
}

func (e *Env) start(cmd *exec.Cmd) *gexec.Session {
	for k, v := range e.vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	session, err := gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return session
}

// Sh runs a shell script with lg first on PATH.
func (e *Env) Sh(script string) *gexec.Session {
	return e.start(exec.CommandContext(ginkgo.GinkgoT().Context(), "sh", "-c", script))
}

// WriteConfig writes config.yaml for rosenhouse/lg served at apiURL, plus any further lines.
func (e *Env) WriteConfig(apiURL string, lines ...string) {
	path, err := config.File(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	yaml := fmt.Sprintf("repo: rosenhouse/lg\napi_url: %s\n", apiURL)
	for _, line := range lines {
		yaml += line + "\n"
	}
	gomega.Expect(os.WriteFile(path, []byte(yaml), 0o644)).To(gomega.Succeed())
}

// Sync runs lg sync to exit and asserts that it left every earlier file under data/ unchanged.
func (e *Env) Sync(args ...string) *gexec.Session {
	ginkgo.GinkgoHelper()
	_, wait := e.StartSync(args...)
	return wait()
}

// StartSync starts lg sync. Its wait func waits for it to exit and asserts as Sync does.
func (e *Env) StartSync(args ...string) (*gexec.Session, func() *gexec.Session) {
	ginkgo.GinkgoHelper()
	before := treesnap.Snapshot(e.Data())
	session := e.Lg(append([]string{"sync"}, args...)...)
	return session, func() *gexec.Session {
		ginkgo.GinkgoHelper()
		gomega.Eventually(session, ExitTimeout).Should(gexec.Exit())
		gomega.Expect(treesnap.Snapshot(e.Data())).To(treesnap.BeAppendOnlyFrom(before))
		return session
	}
}

func (e *Env) Store() string { return e.roots().Store }

func (e *Env) Data() string { return e.roots().Data }

func (e *Env) State() string { return e.roots().State }

func (e *Env) Tmp() string { return e.roots().Tmp }

func (e *Env) roots() config.Roots {
	ginkgo.GinkgoHelper()
	roots, err := config.Locations(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return roots
}

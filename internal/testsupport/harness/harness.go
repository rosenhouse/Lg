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
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// ExitTimeout bounds how long a spec waits for a short-lived lg command.
const ExitTimeout = 10 * time.Second

type Env struct {
	lgPath string
	vars   map[string]string
}

// New gives the calling spec its own HOME and an environment built from
// os.Environ by Scrub.
func New(lgPath string) *Env {
	vars := Scrub(os.Environ(), filepath.Dir(lgPath))
	vars["HOME"] = ginkgo.GinkgoT().TempDir()
	return &Env{lgPath: lgPath, vars: vars}
}

func (e *Env) Home() string { return e.vars["HOME"] }

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

// WriteConfig writes config.yaml for rosenhouse/lg served at apiURL.
func (e *Env) WriteConfig(apiURL string) {
	path, err := config.File(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	yaml := fmt.Sprintf("repo: rosenhouse/lg\napi_url: %s\n", apiURL)
	gomega.Expect(os.WriteFile(path, []byte(yaml), 0o644)).To(gomega.Succeed())
}

// Sync runs lg sync to exit and asserts that it left every earlier file under data/ unchanged.
func (e *Env) Sync(args ...string) *gexec.Session {
	ginkgo.GinkgoHelper()
	before := treesnap.Snapshot(e.Data())
	session := e.Lg(append([]string{"sync"}, args...)...)
	gomega.Eventually(session, ExitTimeout).Should(gexec.Exit())
	gomega.Expect(treesnap.Snapshot(e.Data())).To(treesnap.BeAppendOnlyFrom(before))
	return session
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

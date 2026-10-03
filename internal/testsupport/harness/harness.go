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

func (e *Env) Data() string {
	roots, err := config.Locations(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return roots.Data
}

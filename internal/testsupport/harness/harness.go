// Package harness runs the built lg binary in a scrubbed, per-spec environment.
package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
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
	cmd := exec.CommandContext(ginkgo.GinkgoT().Context(), e.lgPath, args...)
	for k, v := range e.vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	session, err := gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return session
}

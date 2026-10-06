// Package harness runs the built lg binary in a scrubbed, per-spec environment.
package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
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
	vars["LG_TEST_NOW"] = DefaultNow().Format(time.RFC3339)
	gh := fakegh.New(ginkgo.GinkgoT().TempDir())
	vars["LG_GH"] = gh.Path
	return &Env{lgPath: lgPath, vars: vars, gh: gh}
}

func (e *Env) Home() string { return e.vars["HOME"] }

func (e *Env) GH() *fakegh.GH { return e.gh }

func (e *Env) Setenv(key, value string) { e.vars[key] = value }

func (e *Env) Getenv(key string) string { return e.vars[key] }

func (e *Env) Lg(args ...string) *gexec.Session { return e.start(e.Command(args...)) }

// Command gives lg with args in e's environment, not yet started.
func (e *Env) Command(args ...string) *exec.Cmd { return e.command(e.lgPath, args...) }

func (e *Env) command(name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ginkgo.GinkgoT().Context(), name, args...)
	for k, v := range e.vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func (e *Env) start(cmd *exec.Cmd) *gexec.Session {
	session, err := gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return session
}

// Sh runs a shell script with lg first on PATH.
func (e *Env) Sh(script string) *gexec.Session { return e.start(e.command("sh", "-c", script)) }

// WriteConfig writes config.yaml for rosenhouse/lg served at apiURL, plus any further lines.
func (e *Env) WriteConfig(apiURL string, lines ...string) {
	e.writeConfig(append([]string{"repo: rosenhouse/lg", "api_url: " + apiURL}, lines...))
}

func (e *Env) writeConfig(lines []string) {
	path, err := config.File(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)).To(gomega.Succeed())
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

// Status decodes state/status.json.
func (e *Env) Status() map[string]any {
	ginkgo.GinkgoHelper()
	return ReadStatus(filepath.Join(e.State(), "status.json"))
}

// ReadStatus decodes the status.json at path.
func ReadStatus(path string) map[string]any {
	ginkgo.GinkgoHelper()
	raw, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var st map[string]any
	gomega.Expect(json.Unmarshal(raw, &st)).To(gomega.Succeed())
	return st
}

// WriteStatus initializes the store and replaces its state/status.json with raw.
func (e *Env) WriteStatus(raw string) {
	ginkgo.GinkgoHelper()
	writeStatus(e.Store(), filepath.Join(e.State(), "status.json"), raw)
}

// writeStatus initializes the store at root and replaces the status.json at path with raw.
func writeStatus(root, path, raw string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(store.Init(root)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(raw), 0o644)).To(gomega.Succeed())
}

func (e *Env) roots() config.Roots {
	ginkgo.GinkgoHelper()
	roots, err := config.Locations(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return roots
}

// SetNow sets LG_TEST_NOW and fake's clock to now.
func (e *Env) SetNow(now time.Time, fake *fakegithub.Server) {
	e.Setenv("LG_TEST_NOW", now.Format(time.RFC3339))
	fake.SetClock(clock.NewFake(now))
}

// ConfigFile is the path lg reads config.yaml from.
func (e *Env) ConfigFile() string {
	ginkgo.GinkgoHelper()
	path, err := config.File(e.vars)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return path
}

// NewLive gives the calling spec its own LG_HOME and LG_CONFIG, the real gh
// at LG_GH, no LG_TEST_NOW, and the environment ScrubLive keeps.
func NewLive(lgPath string) *Env {
	vars := ScrubLive(os.Environ(), filepath.Dir(lgPath))
	vars["LG_HOME"] = ginkgo.GinkgoT().TempDir()
	vars["LG_CONFIG"] = filepath.Join(ginkgo.GinkgoT().TempDir(), "config.yaml")
	gh, err := exec.LookPath("gh")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	vars["LG_GH"] = gh
	return &Env{lgPath: lgPath, vars: vars}
}

// WriteLiveConfig writes config.yaml for rosenhouse/lg on github.com, plus any further lines.
func (e *Env) WriteLiveConfig(lines ...string) {
	e.writeConfig(append([]string{"repo: rosenhouse/lg"}, lines...))
}

// Start starts lg with args, and kills it when the spec ends.
func (e *Env) Start(args ...string) *gexec.Session {
	session := e.Lg(args...)
	ginkgo.DeferCleanup(func() { session.Kill().Wait(ExitTimeout) })
	return session
}

// PrependPath puts dir first on PATH.
func (e *Env) PrependPath(dir string) {
	e.vars["PATH"] = dir + string(os.PathListSeparator) + e.vars["PATH"]
}

// PathWithout drops from PATH every dir that holds name.
func (e *Env) PathWithout(name string) {
	var kept []string
	for _, dir := range filepath.SplitList(e.vars["PATH"]) {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			kept = append(kept, dir)
		}
	}
	e.vars["PATH"] = strings.Join(kept, string(os.PathListSeparator))
}

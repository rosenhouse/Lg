package harness

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// DefaultNow is the LG_TEST_NOW of each spec and the time fakegithub's clock starts at.
func DefaultNow() time.Time { return recordings.DefaultNow() }

// shortTimeouts let fault specs give up on a stalled fake within seconds.
func shortTimeouts() github.Timeouts {
	return github.Timeouts{Dial: time.Second, TLSHandshake: time.Second, ResponseHeader: time.Second, BodyIdle: time.Second}
}

// InProcessEnv drives mirror.Cycle against its own fake and store.
type InProcessEnv struct {
	FS     *faultfs.FS
	Fake   *fakegithub.Server
	Clock  *clock.Fake
	Mirror *mirror.Mirror
	root   string
}

// InProcess gives the calling spec an empty fakegithub, a store, a clock at
// DefaultNow, and a Mirror of rosenhouse/lg that uses them.
func InProcess() *InProcessEnv {
	ginkgo.GinkgoHelper()
	root := filepath.Join(ginkgo.GinkgoT().TempDir(), "lg")
	gomega.Expect(store.Init(root)).To(gomega.Succeed())
	s, err := store.Open(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	fake := fakegithub.New()
	ginkgo.DeferCleanup(fake.Close)
	api, err := url.Parse(fake.URL())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	clk := clock.NewFake(DefaultNow())
	transport := github.NewTransport(shortTimeouts())
	return &InProcessEnv{
		FS:    faultfs.New(),
		Fake:  fake,
		Clock: clk,
		Mirror: &mirror.Mirror{
			Tokens:    staticToken(fakegh.Token),
			NewGitHub: func(token string) github.Client { return github.NewHTTP(transport, api, "rosenhouse/lg", token) },
			Store:     s,
			Host:      "github.com",
			Repo:      "rosenhouse/lg",
			Clock:     clk,
			LogGrace:  time.Duration(config.Defaults().LogGrace),
		},
		root: root,
	}
}

func (e *InProcessEnv) Data() string { return filepath.Join(e.root, "data") }

func (e *InProcessEnv) Tmp() string { return filepath.Join(e.root, "tmp") }

// AttemptDirs lists the published attempt dirs of a run.
func (e *InProcessEnv) AttemptDirs(runID int64) []string {
	ginkgo.GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(e.Data(), "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", runID), "attempt-*"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return dirs
}

// Tombstones lists every tombstone under data/.
func (e *InProcessEnv) Tombstones() []string {
	ginkgo.GinkgoHelper()
	var found []string
	err := filepath.WalkDir(e.Data(), func(path string, _ fs.DirEntry, err error) error {
		if strings.HasSuffix(path, ".tombstone") {
			found = append(found, path)
		}
		return err
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return found
}

type staticToken string

func (s staticToken) Token(context.Context, string) (string, error) { return string(s), nil }

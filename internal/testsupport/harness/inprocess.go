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
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// DefaultNow is the LG_TEST_NOW of each spec and the time fakegithub's clock starts at.
func DefaultNow() time.Time { return recordings.DefaultNow() }

// shortTimeouts let fault specs give up on a stalled fake within seconds.
func shortTimeouts() github.Timeouts {
	return github.Timeouts{Dial: time.Second, TLSHandshake: time.Second, ResponseHeader: time.Second, BodyIdle: time.Second}
}

// InProcessEnv drives mirror.Cycle against its own fake, and a store on FS.
type InProcessEnv struct {
	FS     *faultfs.FS
	Fake   *fakegithub.Server
	Clock  *clock.Fake
	Mirror *mirror.Mirror
	root   string
}

// InProcess gives the calling spec an empty fakegithub, a store, a clock at
// DefaultNow that both lg and the fake use, and a Mirror of rosenhouse/lg
// that uses them.
func InProcess() *InProcessEnv {
	ginkgo.GinkgoHelper()
	root := filepath.Join(ginkgo.GinkgoT().TempDir(), "lg")
	gomega.Expect(store.Init(root)).To(gomega.Succeed())
	fsys := faultfs.New()
	s, err := store.OpenFS(fsys, root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	fake := fakegithub.New()
	ginkgo.DeferCleanup(fake.Close)
	api, err := url.Parse(fake.URL())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	clk := clock.NewFake(DefaultNow())
	fake.SetClock(clk)
	transport := github.NewTransport(shortTimeouts())
	return &InProcessEnv{
		FS:    fsys,
		Fake:  fake,
		Clock: clk,
		Mirror: &mirror.Mirror{
			Tokens:    staticToken(fakegh.Token),
			NewGitHub: func(token string) github.Client { return github.NewHTTP(transport, api, "rosenhouse/lg", token, clk) },
			Store:     s,
			Host:      "github.com",
			Repo:      "rosenhouse/lg",
			Clock:     clk,
			LogGrace:  time.Duration(config.Defaults().LogGrace),

			ArtifactMaxBytes: int64(config.Defaults().ArtifactMaxBytes),
		},
		root: root,
	}
}

// Sync runs one cycle and asserts that it left every earlier file under data/ unchanged.
func (e *InProcessEnv) Sync(ctx context.Context) error {
	ginkgo.GinkgoHelper()
	before := treesnap.Snapshot(e.Data())
	err := e.Mirror.Cycle(ctx)
	gomega.Expect(treesnap.Snapshot(e.Data())).To(treesnap.BeAppendOnlyFrom(before))
	return err
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

// ArtifactDirs lists the published artifact dirs of a run.
func (e *InProcessEnv) ArtifactDirs(runID int64) []string {
	ginkgo.GinkgoHelper()
	dirs, err := filepath.Glob(filepath.Join(e.Data(), "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", runID), "artifacts", "*"))
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

package mirror_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
)

var _ = Describe("Cycle", Label("sync"), func() {
	DescribeTable("refuses a run of another repository and writes nothing",
		func(fullName string) {
			root := GinkgoT().TempDir()
			storeDir := filepath.Join(root, "store")
			m := mirror.Mirror{
				GitHub: oneRun{fullName: fullName},
				Store:  store.New(filepath.Join(storeDir, "tmp")),
				Data:   filepath.Join(storeDir, "data"),
				Host:   "github.com",
				Repo:   "rosenhouse/lg",
			}

			Expect(m.Cycle(context.Background())).To(MatchError(
				`run 1 belongs to "` + fullName + `", not "rosenhouse/lg"`))
			Expect(os.ReadDir(root)).To(BeEmpty())
		},
		Entry("another repo", "other/lg"),
		Entry("a path out of the store", "../../../../escaped"),
		Entry("no name", ""),
	)
})

// oneRun lists one run and fails every other call.
type oneRun struct{ fullName string }

func (r oneRun) ListRuns(context.Context) ([]github.Run, error) {
	return []github.Run{{Run: model.Run{
		ID:         1,
		Name:       "ci",
		HeadBranch: "main",
		CreatedAt:  time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		Repository: model.Repository{FullName: r.fullName},
	}}}, nil
}

var errUnexpected = errors.New("unexpected call")

func (oneRun) GetAttempt(context.Context, int64, int) (github.Run, error) {
	return github.Run{}, errUnexpected
}

func (oneRun) ListAttemptJobs(context.Context, int64, int) ([]github.Job, error) {
	return nil, errUnexpected
}

func (oneRun) DownloadJobLog(context.Context, int64, io.Writer) error { return errUnexpected }

package index

import (
	"time"

	"github.com/rosenhouse/lg/internal/model"
)

// Rows are a run's rows in every table. Paths are relative to the run dir.
type Rows struct {
	Run        Run
	Attempts   []Attempt
	Jobs       []Job
	Steps      []Step
	Artifacts  []Artifact
	Tombstones []Tombstone
	Units      []string
}

type Run struct {
	Host, Repo    string
	RunID         int64
	CreatedAt     time.Time
	DateDir       string
	WorkflowID    int64
	WorkflowName  string
	HeadBranch    string
	HeadSHA       string
	Event         string
	PRNumbers     []int
	DisplayTitle  string
	LatestAttempt int
}

type Attempt struct {
	Attempt                   int
	Path                      string
	Status, Conclusion        string
	RunStartedAt, CompletedAt time.Time
}

type Job struct {
	JobID                  int64
	Attempt                int
	Name, Slug             string
	Kind                   model.JobKind
	OriginalJobID          int64
	Conclusion             string
	StartedAt, CompletedAt *time.Time
	RunnerName             *string
	Labels                 []string
	HasLog                 bool
	LogBytes               int64
	Path                   string
}

type Step struct {
	JobID                  int64
	Number                 int
	Name, Conclusion       string
	StartedAt, CompletedAt *time.Time
	Path                   string
}

type Artifact struct {
	ArtifactID        int64
	AttributedAttempt int
	Attribution       model.Attribution
	Name              string
	Size              int64
	CreatedAt         time.Time
	Expired, HasZip   bool
	Extracted         bool
	Path              string
}

type Tombstone struct {
	Path         string
	Reason       string
	HTTPStatus   *int
	TombstonedAt time.Time
}

// IndexRun derives a run's rows from the files in runDir alone.
func IndexRun(runDir string) (Rows, error) { return Rows{}, nil }

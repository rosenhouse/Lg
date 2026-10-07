package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/status"
)

type flakesCmd struct {
	Kind    string `default:"all" enum:"rerun,intermittent,all" help:"Report this kind of flake (${enum})."`
	filters `embed:""`
	JSON    bool `name:"json" help:"Print one JSON object per finding."`
}

func (flakesCmd) Help() string {
	return `A name fails in an attempt if any job of that name failed, was cancelled or timed out. Only jobs that ran count. Each finding names a job, or a job and step.

A rerun flip is a name that failed in one attempt of a run and succeeded in another. A carried-forward failure counts as no success. lg judges a job and each of its steps on their own, so one flip often gives a line for the job and a line for the step:
  run 37129390741 (sha 1a51097): "flaky": 1:failure 2:success 3:success; failing steps: "Fail on first attempt only"
  run 37129390741 (sha 1a51097): "flaky" / "Fail on first attempt only": 1:failure 2:success 3:success

An intermittent failure is a name that failed in attempt 1 of a run on the default branch, while attempt 1 of the runs just before and after it succeeded. Runs where the name was skipped or neutral are passed over. A failure next to a run whose attempt 1 is not on disk is not reported. pull_request and pull_request_target runs, and runs whose first or latest attempt was cancelled, are left out. The default branch comes from status.json; --branch replaces it.

--branch, --workflow and --event choose the runs that intermittent failures compare. --sha, --pr, --conclusion, --since and --until choose only which failures are reported. --conclusion matches the latest attempt, --since and --until the start of any attempt, and --job job names.

--json prints kind, run_id, head_sha, job, step, attempts, conclusions, failing_steps and logs for a flip, and kind, workflow_id, workflow, branch, job, step, runs, failures and logs for an intermittent failure.`
}

func (f flakesCmd) Validate() error { return f.validate() }

func (f flakesCmd) Run(deps *Deps) error {
	writeFlip, writeIntermittent := printFlip, printIntermittent
	if f.JSON {
		writeFlip, writeIntermittent = printFlipJSON, printIntermittentJSON
	}
	return query(deps, func(ctx context.Context, ix *index.Index, roots config.Roots) error {
		filter := f.filter(deps.Clock.Now())
		var unread []error
		if f.Kind != "intermittent" {
			flips, err := ix.RerunFlips(ctx, filter)
			unread = append(unread, err)
			for _, flip := range flips {
				if err := writeFlip(deps.Stdout, flip); err != nil {
					return err
				}
			}
		}
		if f.Kind != "rerun" {
			if len(filter.Branches) == 0 {
				branch, err := defaultBranch(deps.Env, roots.State)
				if err != nil {
					return errors.Join(append(unread, err)...)
				}
				filter.Branches = []string{branch}
			}
			found, err := ix.IntermittentFailures(ctx, filter)
			unread = append(unread, err)
			for _, i := range found {
				if err := writeIntermittent(deps.Stdout, i); err != nil {
					return err
				}
			}
		}
		return errors.Join(unread...)
	})
}

// defaultBranch gives the default branch status.json records for the
// configured repository.
func defaultBranch(env map[string]string, state string) (string, error) {
	_, cfg, err := loadConfig(env)
	if err != nil {
		return "", err
	}
	st, err := status.Read(filepath.Join(state, "status.json"))
	if err != nil {
		return "", fmt.Errorf("%w; pass --branch", err)
	}
	var branch string
	if st != nil {
		branch = status.RepoIn(st.Repos, repoKey(cfg)).DefaultBranch
	}
	if branch == "" {
		return "", errors.New("the default branch is unknown until lg sync records it in status.json; pass --branch")
	}
	return branch, nil
}

// flipJSON is a rerun flip as lg flakes --json prints it.
type flipJSON struct {
	Kind         string   `json:"kind"`
	RunID        int64    `json:"run_id"`
	HeadSHA      string   `json:"head_sha"`
	Job          string   `json:"job"`
	Step         *string  `json:"step"`
	Attempts     []int    `json:"attempts"`
	Conclusions  []string `json:"conclusions"`
	FailingSteps []string `json:"failing_steps"`
	Logs         []string `json:"logs"`
}

func printFlipJSON(w io.Writer, flip index.Flip) error {
	out := flipJSON{
		Kind: "rerun", RunID: flip.RunID, HeadSHA: flip.HeadSHA, Job: flip.Job,
		FailingSteps: append([]string{}, flip.FailingSteps...), Logs: append([]string{}, flip.Logs...),
	}
	if flip.Step != "" {
		out.Step = &flip.Step
	}
	for _, o := range flip.Outcomes {
		out.Attempts = append(out.Attempts, o.Attempt)
		out.Conclusions = append(out.Conclusions, o.Conclusion)
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(out)
}

// printFlip prints a line such as
//
//	run 37129390741 (sha 1a51097): "flaky": 1:failure 2:success 3:success; failing steps: "Fail on first attempt only"
func printFlip(w io.Writer, flip index.Flip) error {
	name := fmt.Sprintf("%q", flip.Job)
	if flip.Step != "" {
		name += fmt.Sprintf(" / %q", flip.Step)
	}
	var outcomes []string
	for _, o := range flip.Outcomes {
		outcomes = append(outcomes, fmt.Sprintf("%d:%s", o.Attempt, o.Conclusion))
	}
	line := fmt.Sprintf("run %d (sha %.7s): %s: %s", flip.RunID, flip.HeadSHA, name, strings.Join(outcomes, " "))
	if len(flip.FailingSteps) > 0 {
		var steps []string
		for _, step := range flip.FailingSteps {
			steps = append(steps, fmt.Sprintf("%q", step))
		}
		line += "; failing steps: " + strings.Join(steps, ", ")
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// intermittentJSON is an intermittent failure as lg flakes --json prints it.
type intermittentJSON struct {
	Kind       string        `json:"kind"`
	WorkflowID int64         `json:"workflow_id"`
	Workflow   string        `json:"workflow"`
	Branch     string        `json:"branch"`
	Job        string        `json:"job"`
	Step       *string       `json:"step"`
	Runs       int           `json:"runs"`
	Failures   []failureJSON `json:"failures"`
	Logs       []string      `json:"logs"`
}

type failureJSON struct {
	RunID      int64  `json:"run_id"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
}

func printIntermittentJSON(w io.Writer, s index.Intermittent) error {
	out := intermittentJSON{
		Kind: "intermittent", WorkflowID: s.WorkflowID, Workflow: s.Workflow, Branch: s.Branch, Job: s.Job,
		Runs: len(s.Runs), Logs: []string{},
	}
	if s.Step != "" {
		out.Step = &s.Step
	}
	for _, f := range s.Failures {
		out.Failures = append(out.Failures, failureJSON{RunID: f.RunID, HeadSHA: f.HeadSHA, Conclusion: f.Conclusion})
		out.Logs = append(out.Logs, f.Logs...)
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(out)
}

// printIntermittent prints a line such as
//
//	workflow "ci" on main: "test" / "unit": 1 of 6 runs failed alone: run 18234567890 (sha a1b2c3d) failure
func printIntermittent(w io.Writer, s index.Intermittent) error {
	name := fmt.Sprintf("%q", s.Job)
	if s.Step != "" {
		name += fmt.Sprintf(" / %q", s.Step)
	}
	var runs []string
	for _, f := range s.Failures {
		runs = append(runs, fmt.Sprintf("run %d (sha %.7s) %s", f.RunID, f.HeadSHA, f.Conclusion))
	}
	_, err := fmt.Fprintf(w, "workflow %q on %s: %s: %d of %d runs failed alone: %s\n",
		s.Workflow, s.Branch, name, len(s.Failures), len(s.Runs), strings.Join(runs, ", "))
	return err
}

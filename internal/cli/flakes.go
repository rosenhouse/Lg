package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rosenhouse/lg/internal/index"
)

type flakesCmd struct {
	Kind    string `default:"all" enum:"rerun,all" help:"Report this kind of flake (${enum}). A rerun flip failed in one attempt of a run and passed in another."`
	filters `embed:""`
	JSON    bool `name:"json" help:"Print one JSON object per finding."`
}

func (flakesCmd) Help() string {
	return "Reports each job name, and each step name of it, per run. Only jobs that ran count, as lg paths --unit job selects them. " +
		"A name fails in an attempt if any of its jobs failed, was cancelled or timed out; skipped and neutral count as neither."
}

func (f flakesCmd) Validate() error { return f.validate() }

func (f flakesCmd) Run(deps *Deps) error {
	return query(deps, func(ctx context.Context, ix *index.Index) error {
		flips, unread := ix.RerunFlips(ctx, f.filter(deps.Clock.Now()))
		print := printFlip
		if f.JSON {
			print = printFlipJSON
		}
		for _, flip := range flips {
			if err := print(deps.Stdout, flip); err != nil {
				return err
			}
		}
		return unread
	})
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
//	run 37129390741 (sha 1a51097): "flaky": 1:failure 2:success; failing steps: "Fail on first attempt only"
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
	if flip.Step == "" && len(flip.FailingSteps) > 0 {
		var steps []string
		for _, step := range flip.FailingSteps {
			steps = append(steps, fmt.Sprintf("%q", step))
		}
		line += "; failing steps: " + strings.Join(steps, ", ")
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

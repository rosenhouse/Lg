package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
)

// inserter inserts rows through statements prepared once per transaction.
type inserter struct {
	runs, attempts, jobs, steps, artifacts, tombstones, units *sql.Stmt
}

func newInserter(ctx context.Context, tx *sql.Tx) (*inserter, error) {
	in := &inserter{}
	statements := []struct {
		stmt  **sql.Stmt
		table string
		n     int
	}{
		{&in.runs, "runs", 14},
		{&in.attempts, "attempts", 7},
		{&in.jobs, "jobs", 15},
		{&in.steps, "steps", 7},
		{&in.artifacts, "artifacts", 11},
		{&in.tombstones, "tombstones", 4},
		{&in.units, "units", 1},
	}
	for _, s := range statements {
		placeholders := "?"
		for range s.n - 1 {
			placeholders += ",?"
		}
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+s.table+" VALUES ("+placeholders+")")
		if err != nil {
			return nil, errors.Join(err, in.close())
		}
		*s.stmt = stmt
	}
	return in, nil
}

func (in *inserter) close() error {
	var errs []error
	for _, stmt := range []*sql.Stmt{in.runs, in.attempts, in.jobs, in.steps, in.artifacts, in.tombstones, in.units} {
		if stmt != nil {
			errs = append(errs, stmt.Close())
		}
	}
	return errors.Join(errs...)
}

func (in *inserter) insert(ctx context.Context, runDir string, rows Rows) error {
	at := func(rel string) string { return filepath.Join(runDir, rel) }
	r := rows.Run
	if _, err := in.runs.ExecContext(ctx, r.Host, r.Repo, r.RunID, timeText(r.CreatedAt), r.DateDir, runDir,
		r.WorkflowID, r.WorkflowName, r.HeadBranch, r.HeadSHA, r.Event, jsonText(r.PRNumbers),
		r.DisplayTitle, nullZero(r.LatestAttempt)); err != nil {
		return err
	}
	for _, a := range rows.Attempts {
		if _, err := in.attempts.ExecContext(ctx, r.RunID, a.Attempt, at(a.Path), a.Status, a.Conclusion,
			timeText(a.RunStartedAt), timeText(a.CompletedAt)); err != nil {
			return err
		}
	}
	for _, j := range rows.Jobs {
		if _, err := in.jobs.ExecContext(ctx, j.JobID, r.RunID, j.Attempt, j.Name, j.Slug, string(j.Kind),
			nullZero(j.OriginalJobID), j.Conclusion, timePointer(j.StartedAt), timePointer(j.CompletedAt), j.RunnerName,
			jsonText(j.Labels), j.HasLog, j.LogBytes, at(j.Path)); err != nil {
			return err
		}
	}
	for _, s := range rows.Steps {
		if _, err := in.steps.ExecContext(ctx, s.JobID, s.Number, s.Name, s.Conclusion,
			timePointer(s.StartedAt), timePointer(s.CompletedAt), at(s.Path)); err != nil {
			return err
		}
	}
	for _, a := range rows.Artifacts {
		if _, err := in.artifacts.ExecContext(ctx, a.ArtifactID, r.RunID, nullZero(a.AttributedAttempt), string(a.Attribution),
			a.Name, a.Size, timeText(a.CreatedAt), a.Expired, a.HasZip, a.Extracted, at(a.Path)); err != nil {
			return err
		}
	}
	for _, t := range rows.Tombstones {
		if _, err := in.tombstones.ExecContext(ctx, at(t.Path), t.Reason, t.HTTPStatus, timeText(t.TombstonedAt)); err != nil {
			return err
		}
	}
	for _, u := range rows.Units {
		if _, err := in.units.ExecContext(ctx, at(u)); err != nil {
			return err
		}
	}
	return nil
}

// timeText is t in RFC 3339 UTC, as GitHub writes times, or NULL when zero.
func timeText(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func timePointer(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeText(*t)
}

func nullZero[T int | int64](n T) any {
	if n == 0 {
		return nil
	}
	return n
}

// jsonText is a JSON array, [] when empty, which SQLite's json_each reads.
func jsonText[T any](values []T) string {
	if values == nil {
		return "[]"
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

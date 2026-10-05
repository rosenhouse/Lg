package index

// tables are the tables that hold rows of runs, each with a path column.
var tables = []string{"runs", "attempts", "jobs", "steps", "artifacts", "tombstones", "units"}

const schema = `
CREATE TABLE runs (host TEXT, repo TEXT, run_id INTEGER, created_at TEXT, date_dir TEXT, path TEXT,
	workflow_id INTEGER, workflow_name TEXT, head_branch TEXT, head_sha TEXT, event TEXT, pr_numbers TEXT,
	display_title TEXT, latest_attempt INTEGER);
CREATE TABLE attempts (run_id INTEGER, attempt INTEGER, path TEXT, status TEXT, conclusion TEXT,
	run_started_at TEXT, completed_at TEXT);
CREATE TABLE jobs (job_id INTEGER, run_id INTEGER, attempt INTEGER, name TEXT, slug TEXT, kind TEXT,
	original_job_id INTEGER, conclusion TEXT, started_at TEXT, completed_at TEXT, runner_name TEXT, labels TEXT,
	has_log INTEGER, log_bytes INTEGER, path TEXT);
CREATE TABLE steps (job_id INTEGER, number INTEGER, name TEXT, conclusion TEXT, started_at TEXT,
	completed_at TEXT, path TEXT);
CREATE TABLE artifacts (artifact_id INTEGER, run_id INTEGER, attributed_attempt INTEGER,
	attribution TEXT, name TEXT, size INTEGER, created_at TEXT, expired INTEGER, has_zip INTEGER, extracted INTEGER,
	path TEXT);
CREATE TABLE tombstones (path TEXT, reason TEXT, http_status INTEGER, tombstoned_at TEXT);
CREATE TABLE units (path TEXT, modified INTEGER);
CREATE TABLE meta (format INTEGER);
CREATE INDEX runs_path ON runs (path);
CREATE INDEX attempts_path ON attempts (path);
CREATE INDEX jobs_path ON jobs (path);
CREATE INDEX steps_path ON steps (path);
CREATE INDEX artifacts_path ON artifacts (path);
CREATE INDEX tombstones_path ON tombstones (path);
CREATE INDEX units_path ON units (path);
`

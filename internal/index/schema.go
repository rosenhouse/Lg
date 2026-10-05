package index

const schema = `
CREATE TABLE IF NOT EXISTS runs (host TEXT, repo TEXT, run_id INTEGER, created_at TEXT, date_dir TEXT, path TEXT,
	workflow_id INTEGER, workflow_name TEXT, head_branch TEXT, head_sha TEXT, event TEXT, pr_numbers TEXT,
	display_title TEXT, latest_attempt INTEGER);
CREATE TABLE IF NOT EXISTS attempts (run_id INTEGER, attempt INTEGER, path TEXT, status TEXT, conclusion TEXT,
	run_started_at TEXT, completed_at TEXT);
CREATE TABLE IF NOT EXISTS jobs (job_id INTEGER, run_id INTEGER, attempt INTEGER, name TEXT, slug TEXT, kind TEXT,
	original_job_id INTEGER, conclusion TEXT, started_at TEXT, completed_at TEXT, runner_name TEXT, labels TEXT,
	has_log INTEGER, log_bytes INTEGER, path TEXT);
CREATE TABLE IF NOT EXISTS steps (job_id INTEGER, number INTEGER, name TEXT, conclusion TEXT, started_at TEXT,
	completed_at TEXT, path TEXT);
CREATE TABLE IF NOT EXISTS artifacts (artifact_id INTEGER, run_id INTEGER, attributed_attempt INTEGER,
	attribution TEXT, name TEXT, size INTEGER, created_at TEXT, expired INTEGER, has_zip INTEGER, extracted INTEGER,
	path TEXT);
CREATE TABLE IF NOT EXISTS tombstones (path TEXT, reason TEXT, http_status INTEGER, tombstoned_at TEXT);
CREATE TABLE IF NOT EXISTS units (path TEXT);
CREATE TABLE IF NOT EXISTS meta (format INTEGER);
`

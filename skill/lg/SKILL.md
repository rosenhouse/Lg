---
name: lg
description: Search a local mirror of one GitHub repository's Actions runs, attempts, job logs and artifacts with lg, rg, grep and jq. Use it to find when an error first appeared in CI, which jobs or steps are flaky, what ran on a commit or pull request, or which artifact holds a string, without calling the GitHub API.
---

# lg

`lg` mirrors one repository's GitHub Actions runs, attempts, jobs, logs and artifacts into plain files.
Answer from those files with `lg paths`, `lg where`, `lg flakes`, `rg`, `grep` and `jq`.
Do not call the GitHub API for data that lg mirrors.

## Check freshness first

Always run `lg status` first.
It shows the last sync, the lag, pending units, and why a sync is blocked.
Every lg command also prints `lg: warning: ...` to stderr while the mirror is stale or blocked, or while units stay pending.
A mirror that is behind can miss recent runs, so run `lg sync --wait --timeout 90s` before you conclude there is no match.
It waits for a fresh sync, by the daemon if one runs.
Exit 4 means the sync has not finished, so do not conclude there is no match.

```sh
lg status
lg sync --wait --timeout 90s
```

| Exit | Meaning |
|---|---|
| 0 | ok |
| 1 | error, or units still pending |
| 2 | usage or config error |
| 3 | blocked: auth, rate limit, unreachable or local I/O; `lg status` says why |
| 4 | timeout |

`grep` and `rg` exit 1 when they find no match.
`xargs` exits 123 (1 with macOS xargs) when any grep exits non-zero: no match, or an error that grep printed to stderr.
Read stderr before you conclude there is no match.
With many files, `xargs` runs grep in batches and fails if any batch found nothing, so judge by the output.
Always pass `-r` to `xargs`, so grep does not read stdin when lg prints no paths.

## Install

1. `go install github.com/rosenhouse/lg/cmd/lg@latest`
2. Install ripgrep and jq, as with `brew install ripgrep jq` or `apt install ripgrep jq`. Check that `command -v rg jq` prints two paths, because xargs cannot run a shell alias.
3. Run `gh auth login`. lg takes its token from `gh auth token` and has no token setting.
4. `lg init --repo OWNER/NAME` writes the config. For GitHub Enterprise Server, run `lg init --repo OWNER/NAME --host HOST`.
5. `lg sync` fetches the last 7 days.
6. `lg daemon install` installs a systemd user unit or a launchd agent that syncs every 10 minutes.
7. `lg skill install` installs this skill into `${CLAUDE_CONFIG_DIR:-~/.claude}/skills/lg/`.

## Layout

`lg root` prints the data dir.

```text
<host>/<owner>/<repo>/runs/<YYYY-MM-DD>/<run_id>_<workflow>_<branch>/
  attempt-<N>/                  one dir per completed attempt
    attempt.json                the run as of attempt N
    jobs.json                   every job of attempt N
    artifacts.json              the run's artifact listing when attempt N was fetched
    fetch.json                  how lg fetched it, with run_created_at
    jobs/<job_id>_<job>/
      job.json                  this job's element of jobs.json
      log.txt                   the job's log, byte for byte
      log.txt.tombstone         instead of log.txt when the log is gone for good
  artifacts/<artifact_id>_<name>/
    artifact.json               this artifact's element of the listing
    artifact.zip                the zip as GitHub served it
    artifact.zip.tombstone      instead of artifact.zip when it is gone or too large
    fetch.json
    extracted/                  written by lg extract
```

- `<owner>/<repo>` takes GitHub's spelling of the repository's name, whatever case the config uses, so take paths from `lg paths` or a glob rather than typing them.
- The date dir is the UTC date the run was created. A rerun stays under that date.
- Names keep `[A-Za-z0-9.-]`, so branch `feat/retry upload` becomes `feat-retry-upload`. Each name is also trimmed of leading and trailing `-` and `.`, cut to 60 bytes, and `none` when nothing is left, so match a long name with a glob on its start. The id before the first `_` is exact.
- A dir that exists is complete, and files never change. Expiry and eviction remove whole dirs.
- JSON files hold GitHub's API bodies, re-indented with two spaces, so `rg --no-config '"head_sha": "1a51097'` finds a commit. Objects keep GitHub's shape, so their `jq` paths match the GitHub REST docs.
- `jobs.json` and `artifacts.json` hold one array of every page's elements, without GitHub's `total_count` wrapper, so use `jq '.[]'`.
- Each `log.txt` starts with a UTF-8 BOM, and every line starts with GitHub's timestamp prefix and a space, as in `2026-10-03T14:22:57.6677717Z ##[error]...`. Anchor patterns after it with `^[^ ]+ `, and drop it with `cut -d' ' -f2-`.

Run `rg --no-config`, so that a ripgrep config file named by `RIPGREP_CONFIG_PATH` cannot change its output.
When rg walks the store, pass `-uu`, so that it searches hidden files and no ignore file hides any.
Chain `lg root` and `cd` with `&&`, as below, so a script stops when lg fails instead of searching the current dir.

```sh
data=$(lg root) && cd "$data" && rg --no-config -uu -l '"head_sha": "1a51097' --glob attempt.json
lg paths --unit log -0 | xargs -0 -r grep -hE '^[^ ]+ ##\[error\]' | cut -d' ' -f2- | sort | uniq -c | sort -rn
```

## lg paths

`lg paths` prints the mirrored files that match its filters, one per line, or NUL-separated with `-0`.
It prints only files that exist, never tombstones.

- `lg paths --branch main --branch release-3 --sha 1a51097 --pr 42 --workflow lg-fixture --job 'build*' --event push --conclusion failure` shows every filter. `--sha` takes a prefix and `--job` a glob.
- Filters may repeat. Repeats of one flag match any value, and different flags must all match.
- `--since` and `--until` take `30d`, `12h`, `2026-09-01` (UTC) or RFC 3339, as in `lg paths --since 30d --until 2026-10-01`. A date means its 00:00 UTC, so `--until 2026-10-01` stops at the start of October 1.
- `--branch` skips runs from forks.
- `--pr` misses fork pull_request runs, because GitHub lists no pull requests for them; find those with `lg paths --event pull_request` or `lg paths --sha 1a51097`.

| Unit | Prints |
|---|---|
| `lg paths` | `log.txt` files and what `--unit extracted` prints |
| `lg paths --unit run` | every `.json` under the run dir, outside `extracted/` |
| `lg paths --unit attempt` | `attempt.json`, `jobs.json`, `artifacts.json` and `fetch.json` |
| `lg paths --unit job` | `job.json` |
| `lg paths --unit log` | `log.txt` |
| `lg paths --unit artifact` | `artifact.zip` |
| `lg paths --unit extracted` | the files under `extracted/`, without `.lg-extract.json` |

`--since` and `--until` compare with the run's creation for `--unit run`, the attempt's start for attempt, job and log units, and the artifact's creation for artifact and extracted units.
`--conclusion` compares with the job's conclusion for job and log units, the attempt's for attempt units, and the latest attempt's otherwise.

## Error archaeology

When did `foo bar` first appear on main or release-3?
The run's date and id are in each path.

```sh
lg paths --branch main --branch release-3 -0 | xargs -0 -r rg --no-config -l 'foo bar' \
  | sed -E 's#.*/runs/([0-9-]+)/([0-9]+)_.*#\1 run \2#' | sort -u
lg paths --branch main --branch release-3 --since 30d -0 | xargs -0 -r rg --no-config -Hn 'foo bar'
```

A rerun's hits sit under the run's creation date; the timestamp prefix on the hit line, or `run_started_at` in the attempt's `attempt.json`, says when it ran.
Pass `-H` to rg or grep, so a batch of one file still prints its path.
`lg where` decodes a path or an `rg -Hn` hit into JSON: run, attempt, job, SHA, PRs, conclusions and the GitHub URL.

```sh
lg paths --branch release-3 -0 | xargs -0 -r rg --no-config -Hn 'foo bar' | lg where \
  | jq -r '[.run_id, .attempt, .job, .artifact, .sha[0:7], .html_url] | @tsv'
```

## Flakes

`lg flakes` reports flakes per job name and per (job name, step name).
`lg flakes --kind rerun` finds a job or step that failed in one attempt of a run and passed in another, on the same SHA.
An attempt that carries forward a failed job or step gives its name no success.
`lg flakes --kind intermittent` finds an attempt 1 on the default branch that failed while the runs before and after it passed.
It leaves out `pull_request` and `pull_request_target` runs, and runs whose first or latest attempt was cancelled, so use `--kind rerun` for pull requests.
A run whose attempt 1 is not on disk yet leaves no failure next to it alone.
`lg flakes --branch release-3` replaces the default branch, and lg exits 1 asking for `--branch` when it does not know the default.
`--job` selects job names. The other filters select runs: `--since` and `--until` match the start of any attempt, and `--conclusion` the latest attempt.
For `--kind intermittent`, `--branch`, `--workflow` and `--event` pick the runs of each series, while `--sha`, `--pr`, `--conclusion`, `--since` and `--until` pick only which failures are reported.
Failing means `failure`, `cancelled` or `timed_out`, and only jobs that ran count.
A step can flip while its job does not.
A `continue-on-error` step that fails still reports success, so lg flakes never sees it; grep its log for `##[error]`.

```sh
lg flakes --kind rerun --sha 1a51097
lg flakes --kind rerun --json | jq -r 'select(.run_id == 37129390741) | [.job, .step // "", (.conclusions | join(" "))] | @tsv'
lg flakes --kind intermittent --branch main --since 30d
```

## A commit or a pull request

```sh
lg paths --sha 1a51097 --unit attempt -0 | xargs -0 -r jq -r 'select(input_filename | endswith("/attempt.json")) | [.id, .run_attempt, .conclusion, .run_started_at] | @tsv'
lg paths --sha 1a51097 --unit attempt -0 | xargs -0 -r jq -r 'select(input_filename | endswith("/jobs.json")) | .[] | select(.conclusion == "failure") | [.run_id, .run_attempt, .name] | @tsv'
lg paths --pr 42 --unit attempt -0 | xargs -0 -r jq -r 'select(input_filename | endswith("/attempt.json")) | [.id, .run_attempt, .event, .conclusion] | @tsv'
lg paths --pr 42 --unit job -0 | xargs -0 -r jq -r '[.run_attempt, .name, .conclusion] | @tsv'
```

## Reruns and carried-forward jobs

After "Re-run failed jobs", the new attempt lists every job with a new id.
A job that did not rerun is carried forward: it has `job.json` and no log.
A carried-forward job's log is under the attempt that ran it, and `lg where` on its `job.json` names that log in `original_log`.

```sh
lg paths --unit job | lg where | jq -r 'select(.carried_forward) | [.job, .original_log] | @tsv'
```

Artifacts belong to the run, under `artifacts/`.
Each attempt's `artifacts.json` is a snapshot of the run's listing when lg fetched that attempt, not a list of what the attempt uploaded.
`lg where` on an artifact path gives `attributed_attempt` and how it was decided in `attribution`.

## Artifacts

Zips are not searched until lg extract expands them into `extracted/` beside each zip.
Give it filters, paths, or `lg extract --all`.
A nested zip, tar or tar.gz stays beside its expansion `<name>.d/`, so rg may report "binary file matches" for the archive, and `lg where` skips that line. Read the `.d/` text instead, and pass `-I` to grep to skip binary files.
`lg where` on an extracted file adds `inner_path`, its path below `extracted/`.
A member named `.ignore`, `.rgignore` or `.gitignore` is renamed `<name>~lg`, so rg still searches its tree.
`extracted/.lg-extract.json` records every renamed or skipped member.
Artifacts often hold hidden dirs such as `.pytest_cache/`, which rg walks only with `-uu`.

```sh
lg extract --branch release-3
lg paths --unit extracted -0 | xargs -0 -r grep -lI 'nested in tar.gz' | lg where \
  | jq -r '[.run_id, .artifact_id, .artifact, .inner_path] | @tsv'
data=$(lg root) && cd "$data" && rg --no-config -uu -l 'test_retry'
```

## Gaps

A `<file>.tombstone` replaces a file that lg will never have.
Its `reason` is `expired`, `deleted`, `not_applicable` or `too_large`.
`not_applicable` marks a job that produces no log, such as a skipped one.
`too_large` marks a zip over `artifact_max_bytes` (500MB by default). Raising the limit later does not fetch it.
A unit that failed for a transient reason, such as a GitHub outage, is pending: `lg status` lists it, and the next sync retries it.

```sh
data=$(lg root) && cd "$data" && rg --no-config -uu -l '"reason": "deleted"' --glob '*.tombstone'
```

## Expiry and eviction

Each sync removes the runs whose date dir is older than `retention` (90 days by default), so a late rerun ages out with its run.
Past `disk_cap` (50GB by default), lg removes `extracted/` dirs first, oldest run first, and then whole runs, oldest date first.
Eviction records a horizon in `lg status`, and lg never fetches a run created at or before the horizon again.
So a run younger than `retention` can be gone; check the horizon before you conclude there is no match.
Run `lg extract --branch release-3`, or whatever filters you used, again to restore evicted `extracted/` dirs whose zips remain.
`lg gc --dry-run` prints the dirs that expiry and eviction would remove now.
If `lg paths` or `lg flakes` fails on its index, `lg index rebuild` rebuilds it from the files.

```sh
lg status | grep -E '^ *(horizon|retention):'
lg gc --dry-run
lg index rebuild
```

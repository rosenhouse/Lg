# lg

lg mirrors one GitHub repository's Actions runs, attempts, job logs and artifacts into plain files.
You and your coding agents then answer CI questions from disk with `rg`, `grep` and `jq`, such as when an error first appeared or which jobs are flaky.
A daemon keeps the mirror up to date.
lg runs on Linux and macOS against github.com.
GitHub Enterprise Server is tested only against a fake ([#3 §13](https://github.com/rosenhouse/lg/issues/3)); to try it, run `gh auth login --hostname HOST` and `lg init --repo OWNER/NAME --host HOST`.

## Install

lg needs Go 1.21 or later and [gh](https://cli.github.com). The examples below use [rg](https://github.com/BurntSushi/ripgrep) and [jq](https://jqlang.org).
Go 1.21 to 1.24 first download Go 1.25.0, which needs network access; `GOTOOLCHAIN` controls this.

```sh
go install github.com/rosenhouse/lg/cmd/lg@latest
```

From a clone, run `go install ./cmd/lg`.
go install puts lg in `$GOBIN`, or in `$(go env GOPATH)/bin` when GOBIN is unset; add that dir to PATH.

## Quick start

lg takes its token from `gh auth token`, so log in to gh first.

```sh
gh auth login
lg init --repo OWNER/NAME
lg sync
lg status
```

`lg sync` fetches the runs created within `backfill`, 7d by default. To fetch more, add `backfill: 30d` to `~/.config/lg/config.yaml`; it must not exceed `retention`.
After a pause in syncing longer than `backfill`, lg does not fetch the runs created more than `backfill` before the next sync; raise `backfill` to cover them.
The first `lg sync` can take several minutes. It warns that lg never synced, then prints nothing more unless it fails.
`lg status` shows the last sync, the lag, pending units and why syncs are blocked.
A unit is a run, attempt, job, log, artifact or `extracted/` dir.
The lag is the time from the newest completed run's creation to the last sync's finish, so it grows with each sync that finds no newer run.

## Daemon

`lg daemon install` runs a systemd user unit or launchd agent that syncs every `sync_interval`, 10m by default.
Without a systemd user manager or launchd, as in a container, run `lg daemon run` under your own supervisor.
Every command warns while the last successful sync is older than twice `sync_interval`; `lg status --help` says when else it warns.
With the daemon running, `lg sync` only asks for a sync; `lg sync --wait` also waits for it.
If `lg status` shows syncs blocked by `auth` after `lg daemon install`, the service may not reach gh's keyring, and `gh auth login --insecure-storage` usually fixes it.
The daemon logs to `journalctl --user -u lg` on Linux and to the store's `state/daemon.log` on macOS.
On a headless Linux machine, run `loginctl enable-linger` so the unit outlives your login.
`lg daemon uninstall` removes the service.

## Search

Search the logs of main from the last 7 days, and decode each hit into JSON:

```sh
lg grep --branch main --since 7d 'foo bar'
lg grep --branch main --since 7d --json 'foo bar' | jq -c 'del(.path)'
```

The pattern is a Go regular expression, and `lg grep --help` lists the flags.
lg grep exits 5 when no line matches.
For rg's other features, such as context lines, pipe `lg paths` to rg, and decode its hits with `lg where`:

```sh
lg paths --branch main --since 7d -0 | xargs -0 -r rg --no-config -Hn -C2 'foo bar'
lg paths --branch main --since 7d -0 | xargs -0 -r rg --no-config -Hn 'foo bar' | lg where | jq -c 'del(.path)'
```

`rg --no-config` keeps your ripgrep config from changing the hit format that `lg where` reads.
When rg finds no match in a batch of files, xargs exits 123, or 1 on macOS, so judge by the output and stderr.

Find flaky jobs and steps:

```sh
lg flakes
```

Search inside artifacts, after expanding their zips:

```sh
lg extract --branch main
lg grep --unit extracted 'foo bar'
```

Teach Claude Code to do all this:

```sh
lg skill install
```

`lg --help` lists every command, and `lg paths --help`, for example, explains one.

## Where the data lives

| What | Default | Override |
|---|---|---|
| Config | `~/.config/lg/config.yaml` | `LG_CONFIG` names the file. `XDG_CONFIG_HOME` replaces `~/.config`. |
| Store | `~/.local/share/lg` | `LG_HOME` names the dir. `XDG_DATA_HOME` replaces `~/.local/share`. |

`LG_HOME` does not move the config.

`lg root` prints the store's `data/` dir, which holds each run like this:

```text
<host>/<owner>/<repo>/runs/<date>/<run_id>_<workflow>_<branch>/
  attempt-N/
    attempt.json  jobs.json
    jobs/<job_id>_<job>/
      job.json  log.txt
  artifacts/<artifact_id>_<artifact>/
    artifact.json  artifact.zip
    extracted/
```

`<owner>/<repo>` uses GitHub's spelling, which may differ in case from config.yaml.
`<date>` is the run's UTC creation date.
`<workflow>`, `<branch>`, `<job>` and `<artifact>` are slugs, so branch `feat/x` becomes `feat-x`.
A `<file>.tombstone`, such as `log.txt.tombstone`, replaces a file that lg will never have.
Each `attempt-N/` and `extracted/` dir is complete once it appears, and files never change.
lg removes runs older than `retention`, 90d by default.
While `data/` exceeds `disk_cap`, 50GB by default, lg removes `extracted/` trees first, then the oldest runs.

## Design

The design decisions are in GitHub issues:
[product](https://github.com/rosenhouse/lg/issues/1),
[engineering](https://github.com/rosenhouse/lg/issues/2),
[storage layout and sync](https://github.com/rosenhouse/lg/issues/3), and
[M1 plan, architecture and test harness](https://github.com/rosenhouse/lg/issues/4).

## Develop

`make lint test build` needs [golangci-lint](https://golangci-lint.run) v2.5.0.
`go tool ginkgo -r --label-filter=<feature>` runs the specs of one feature, such as `extract`.
Never run `go get -u`, because newer dependencies need a Go that golangci-lint v2.5.0 refuses.

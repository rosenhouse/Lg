# lg

lg mirrors one GitHub repository's Actions runs, attempts, job logs and artifacts into plain files.
You and your coding agents then answer CI questions from disk with `rg`, `grep` and `jq`, such as when an error first appeared or which jobs are flaky.
A daemon keeps the mirror up to date.
lg runs on Linux and macOS against github.com. GitHub Enterprise Server is tested only against a fake; see [#3 §13](https://github.com/rosenhouse/lg/issues/3).

## Install

lg needs Go 1.21 or later, [gh](https://cli.github.com), [rg](https://github.com/BurntSushi/ripgrep) and [jq](https://jqlang.org).

```sh
go install github.com/rosenhouse/lg/cmd/lg@latest
```

From a clone, run `go install ./cmd/lg`.
Go before 1.25 first downloads a newer Go, which needs network access; `GOTOOLCHAIN` controls this.
go install puts lg in `$GOBIN`, or in `$(go env GOPATH)/bin` when GOBIN is unset; add that dir to PATH.

## Quick start

lg takes its token from `gh auth token`, so log in to gh first.

```sh
gh auth login
lg init --repo OWNER/NAME
lg sync
lg daemon install
lg status
```

`lg sync` fetches the runs created within `backfill`, 7d by default. To fetch more, add `backfill: 30d` to `~/.config/lg/config.yaml`; it must not exceed `retention`.
Each sync lists only the runs created within `backfill`, so lg never fetches a run created during a pause in syncing longer than that.
Raise `backfill` before you sync to cover the pause.
The first `lg sync` can take several minutes. It warns that lg never synced, then prints nothing more unless it fails.
`lg status` shows the last sync, the lag, pending units and why syncs are blocked.
To try GitHub Enterprise Server, run `gh auth login --hostname HOST` and `lg init --repo OWNER/NAME --host HOST`.

## Daemon

`lg daemon install` is optional. It runs a systemd user unit or launchd agent that syncs every `sync_interval`, 10m by default.
Without a systemd user manager or launchd, as in a container, run `lg daemon run` under your own supervisor.
Without the daemon, every command warns once the last successful sync is older than twice `sync_interval`, and a successful `lg sync` clears it.
With the daemon running, `lg sync` only asks for a sync; `lg sync --wait` also waits for it.
If `lg status` shows syncs blocked by `auth` after `lg daemon install`, the service may not reach gh's keyring.
`lg status` names the fix, usually `gh auth login --insecure-storage`.
The daemon logs to `journalctl --user -u lg` on Linux and to the store's `state/daemon.log` on macOS.
On a headless Linux machine, run `loginctl enable-linger` so the unit outlives your login.
`lg daemon uninstall` removes the service.

## Search

Search the logs and extracted artifacts of main from the last 7 days, and decode each hit:

```sh
lg paths --branch main --since 7d -0 | xargs -0 -r rg --no-config -Hn 'foo bar'
lg paths --branch main --since 7d -0 | xargs -0 -r rg --no-config -Hn 'foo bar' | lg where | jq -c 'del(.path)'
```

`rg --no-config` keeps your ripgrep config from changing the hit format that `lg where` reads.
When rg finds no match in a batch of files, xargs exits 123, or 1 on macOS, so judge by the output and stderr.

Find flaky jobs and steps:

```sh
lg flakes
```

lg flakes judges a job and each of its steps on their own, so one flip often gives a line for the job and a line for the step.

Search inside artifacts, after expanding their zips:

```sh
lg extract --branch main
lg paths --unit extracted -0 | xargs -0 -r rg --no-config -Hn 'foo bar'
```

lg extract says "nothing to extract" when no artifact.zip matches its filters, or when each one is extracted already or its zip was gone or too large to fetch.

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

`lg root` prints the store's `data/` dir, which holds each run at `<host>/<owner>/<repo>/runs/<date>/<run_id>_<workflow>_<branch>/`.
`<owner>/<repo>` is spelled as GitHub spells the repository's full name, while config.yaml and `lg status` keep the spelling given to `lg init --repo OWNER/NAME`.
`<date>` is the run's UTC creation date.
`<workflow>` and `<branch>` are slugs, so branch `feat/x` becomes `feat-x`.
Each `attempt-N/` and `extracted/` dir is complete once it appears, and files never change.
A run dir gains attempts and artifacts as they finish, and an artifact dir gains `extracted/` when lg extract expands it.
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

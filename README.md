# lg

lg mirrors one GitHub repository's Actions runs, attempts, job logs and artifacts into plain files.
You and your coding agents then answer CI questions from disk with `rg`, `grep` and `jq`, such as when an error first appeared or which jobs are flaky.
A daemon keeps the mirror within about 10 minutes of GitHub.
lg runs on Linux and macOS against github.com. GitHub Enterprise Server support is untested; see [#3 §13](https://github.com/rosenhouse/lg/issues/3).

## Install

lg needs Go 1.25 or later, [gh](https://cli.github.com), [rg](https://github.com/BurntSushi/ripgrep) and [jq](https://jqlang.org).

```sh
go install github.com/rosenhouse/lg/cmd/lg@latest
```

From a clone, run `go install ./cmd/lg`.

## Quick start

lg takes its token from `gh auth token`, so log in to gh first.

```sh
gh auth login
lg init --repo OWNER/NAME
lg sync
lg daemon install
lg status
```

`lg sync` fetches the runs of the last 7 days; set `backfill` in config.yaml to fetch more. The first `lg sync` can take several minutes and prints nothing until it finishes.
`lg daemon install` runs a systemd user unit or launchd agent that syncs every 10 minutes.
`lg status` shows its last sync and any warning.
The daemon logs to `journalctl --user -u lg` on Linux and to the store's `state/daemon.log` on macOS.
On a headless Linux machine, run `loginctl enable-linger` so the unit outlives your login.
To try GitHub Enterprise Server, run `gh auth login --hostname HOST` and `lg init --repo OWNER/NAME --host HOST`.

Search the logs of main from the last 30 days, and decode each hit:

```sh
lg paths --branch main --since 30d -0 | xargs -0 -r rg -Hn 'foo bar'
lg paths --branch main --since 30d -0 | xargs -0 -r rg -Hn 'foo bar' | lg where | jq -c 'del(.path)'
```

Find flaky jobs and steps:

```sh
lg flakes
```

Search inside artifacts, after expanding their zips:

```sh
lg extract --branch main
lg paths --unit extracted -0 | xargs -0 -r rg -Hn 'foo bar'
```

Teach Claude Code to do all this:

```sh
lg skill install
```

`lg --help` lists every command, and `lg paths --help`, for example, explains one.

## Where the data lives

| What | Default | Override |
|---|---|---|
| Config | `~/.config/lg/config.yaml` | `LG_CONFIG`, or `XDG_CONFIG_HOME` |
| Store | `~/.local/share/lg` | `LG_HOME`, or `XDG_DATA_HOME` |

`lg root` prints the store's `data/` dir.
Runs are under `<host>/<owner>/<repo>/runs/<date>/<run_id>_<workflow>_<branch>/`.
`<owner>/<repo>` is spelled as GitHub spells the repository's full name.
Each `attempt-N/`, artifact dir and `extracted/` dir is complete once it appears, and its files never change.
A run dir gains attempts and artifacts as they finish, and an artifact dir gains `extracted/` when lg extract expands it.
Runs older than 90 days are removed. While `data/` exceeds 50GB, lg removes `extracted/` trees first, then the oldest runs.

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

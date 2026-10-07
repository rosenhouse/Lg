# lg

lg mirrors one GitHub repository's Actions runs, attempts, job logs and artifacts into plain files.
You and your coding agents then answer CI questions from disk with `rg`, `grep` and `jq`, such as when an error first appeared or which jobs are flaky.
A daemon keeps the mirror within about 10 minutes of GitHub.
lg runs on Linux and macOS, against github.com or GitHub Enterprise Server.

## Install

lg needs Go, [gh](https://cli.github.com), [rg](https://github.com/BurntSushi/ripgrep) and [jq](https://jqlang.org).

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

`lg sync` fetches the runs of the last 7 days. `lg daemon install` runs a systemd user unit or launchd agent that syncs every 10 minutes.

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
A dir there is complete once it appears, and its files never change.
Runs older than 90 days are removed, and the oldest data goes first while `data/` exceeds 50GB.

## Design

The design decisions are in GitHub issues:
[product](https://github.com/rosenhouse/lg/issues/1),
[engineering](https://github.com/rosenhouse/lg/issues/2), and
[storage layout and sync](https://github.com/rosenhouse/lg/issues/3).

#!/usr/bin/env bash
# record.sh RUN_ID LABEL — snapshot Actions API responses for a run.
# GITHUB_API_URL points it at a fakegithub.
set -euo pipefail
shopt -s nullglob
run=$1 label=$2
api=${GITHUB_API_URL:-https://api.github.com}/repos/rosenhouse/lg/actions
accept='Accept: application/vnd.github+json'
root=$(cd "$(dirname "$0")" && pwd)
out=$root/run-$run/$label

fail() { echo "record.sh: $*" >&2; exit 1; }
get() { # get PATH FILE — JSON GET, records status
  local code
  code=$(curl -sS -o "$2" -w '%{http_code}' -H "$accept" "$api/$1")
  [ "$code" = 200 ] || fail "$code $1"
  echo "$code $1" >> status.txt
}
fetch() { # fetch PATH FILE — follows redirect, records first-hop status and final status
  local first final
  first=$(curl -sS -o /dev/null -w '%{http_code}' "$api/$1")
  case $first in 302 | 404 | 410) ;; *) fail "$first $1" ;; esac
  final=$(curl -sSL -o "$2" -w '%{http_code}' "$api/$1")
  echo "$first->$final $1" >> status.txt
}

# The stage is assembled in a temp dir and moved into place once complete.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
code=$(curl -sS -o run.json -D headers -w '%{http_code}' -H "$accept" "$api/runs/$run")
[ "$code" = 200 ] || fail "$code runs/$run: $(jq -r '.message // empty' run.json 2>/dev/null || head -c 200 run.json)"
echo "$code runs/$run" > status.txt
# The run GET's Date header dates the recording.
grep -i '^date:' headers | cut -d' ' -f2- | tr -d '\r' > recorded_at.txt
rm headers
get "runs/$run/jobs?filter=all&per_page=100" jobs-all.json
get "runs/$run/jobs?filter=latest&per_page=100" jobs-latest.json
get "runs/$run/artifacts?per_page=100" artifacts.json
n=$(jq .run_attempt run.json)
for a in $(seq 1 "$n"); do
  d=attempt-$a
  mkdir -p "$d/logs"
  get "runs/$run/attempts/$a" "$d/attempt.json"
  get "runs/$run/attempts/$a/jobs?per_page=100" "$d/jobs.json"
  fetch "runs/$run/attempts/$a/logs" "$d/logs.zip"
  jobs=$(jq -r '.jobs[].id' "$d/jobs.json")
  for j in $jobs; do
    fetch "jobs/$j/logs" "$d/logs/$j.txt"
  done
done
mkdir -p artifacts
# Ids come from every stage, so expired and deleted zips are recorded too.
ids=$(jq -r '.artifacts[]?.id' artifacts.json "$root/run-$run"/*/artifacts.json | sort -nu)
for id in $ids; do
  fetch "artifacts/$id/zip" "artifacts/$id.zip"
done
mkdir -p "$(dirname "$out")"
rm -rf "$out"
mv "$tmp" "$out"
echo "recorded $label: run $run, $n attempts"

#!/usr/bin/env bash
# record.sh RUN_ID LABEL — snapshot Actions API responses for a run.
# GITHUB_API_URL points it at a fakegithub.
set -euo pipefail
run=$1 label=$2
api=${GITHUB_API_URL:-https://api.github.com}/repos/rosenhouse/lg/actions
accept='Accept: application/vnd.github+json'
out=$(dirname "$0")/run-$run/$label

get() { # get PATH FILE — JSON GET, records status
  local code
  code=$(curl -sS -o "$2" -w '%{http_code}' -H "$accept" "$api/$1")
  echo "$code $1" >> status.txt
}
fetch() { # fetch PATH FILE — follows redirect, records first-hop status and final status
  local first final
  first=$(curl -sS -o /dev/null -w '%{http_code}' "$api/$1")
  final=$(curl -sSL -o "$2" -w '%{http_code}' "$api/$1")
  echo "$first->$final $1" >> status.txt
}

# Nothing is written until the run GET succeeds. Its Date header dates the recording.
tmp=$(mktemp -d)
code=$(curl -sS -o "$tmp/run.json" -D "$tmp/headers" -w '%{http_code}' -H "$accept" "$api/runs/$run")
[ "$code" = 200 ] || { echo "record.sh: $code runs/$run: $(jq -r .message "$tmp/run.json")" >&2; exit 1; }
mkdir -p "$out"
cd "$out"
echo "$code runs/$run" > status.txt
mv "$tmp/run.json" run.json
grep -i '^date:' "$tmp/headers" | cut -d' ' -f2- | tr -d '\r' > recorded_at.txt
rm -r "$tmp"
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
  for j in $(jq -r '.jobs[].id' "$d/jobs.json"); do
    fetch "jobs/$j/logs" "$d/logs/$j.txt"
  done
done
mkdir -p artifacts
# Ids come from every stage, so expired and deleted zips are recorded too.
for id in $(jq -r '.artifacts[]?.id' ../*/artifacts.json | sort -nu); do
  fetch "artifacts/$id/zip" "artifacts/$id.zip"
done
echo "recorded $label: run $run, $n attempts"

#!/usr/bin/env bash
# Prints the bats files of one shard of the review workflow suites
# (tests/bats/workflow/ and redaction-replay.bats). CI runs each shard as its
# own job (test-actions.yml): the suites render a lot, and one runner needed
# about ten minutes for all of them.
#
#   workflow-shards.sh <shard>            the files of the shard, one per line
#   workflow-shards.sh --filter <shard>   the `bats --filter` of the shard, or nothing
#   workflow-shards.sh --list             the shard names
#
# The shard "rest" is every file that no other shard names. A new test file
# runs there without a change to this script, so no file can drop out of CI.
#
# A file whose tests must not run at the same time (subset-equivalence.bats:
# one shared fixture repository) can be in several shards, each with a filter
# on the test names. Every test of such a file must match exactly one of the
# filters; the script fails when a test matches none or two, so a new test
# cannot drop out either.
#
# Balance the shards with the per-test times that `bats --timing` prints.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

declare -A shards=(
  [cli]="tests/bats/workflow/cli.bats
tests/bats/workflow/native-discover.bats
tests/bats/workflow/native-render.bats"
  [actions]="tests/bats/workflow/review-action.bats
tests/bats/workflow/root-action.bats
tests/bats/workflow/classify.bats
tests/bats/workflow/scope-comment.bats"
  [equivalence-1]="tests/bats/workflow/subset-equivalence.bats"
  [equivalence-2]="tests/bats/workflow/subset-equivalence.bats"
)

# bats --filter: an extended regular expression on the test name
declare -A filters=(
  [equivalence-1]='^equivalence: [a-m]'
  [equivalence-2]='^equivalence: [^a-m]|^[^e]'
)

all_files() {
  {
    find tests/bats/workflow -name '*.bats'
    echo tests/bats/redaction/redaction-replay.bats
  } | sort
}

assigned_files() {
  local name
  for name in "${!shards[@]}"; do
    echo "${shards[$name]}"
  done | sort -u
}

fail() {
  echo "workflow-shards.sh: $*" >&2
  exit 1
}

# the checks that keep every test in exactly one job
check() {
  local file name test matches
  # a shard must not name a file that is gone: it would hide a renamed suite
  while read -r file; do
    [ -f "$file" ] || fail "$file does not exist"
  done < <(assigned_files)
  for name in "${!shards[@]}"; do
    [ -z "${filters[$name]:-}" ] && continue
    # a filter applies to every file of its shard: keep such a shard to one file
    [ "$(wc -l <<<"${shards[$name]}")" -eq 1 ] || fail "shard $name has a filter and more than one file"
  done
  while read -r file; do
    # the shards that run this file
    local owners=()
    for name in "${!shards[@]}"; do
      grep -qxF "$file" <<<"${shards[$name]}" && owners+=("$name")
    done
    [ "${#owners[@]}" -gt 1 ] || { [ -z "${filters[${owners[0]}]:-}" ] || fail "$file: the only shard ${owners[0]} has a filter"; continue; }
    while read -r test; do
      matches=0
      for name in "${owners[@]}"; do
        [ -n "${filters[$name]:-}" ] || fail "$file is in several shards, but shard $name has no filter"
        [[ "$test" =~ ${filters[$name]} ]] && matches=$((matches + 1))
      done
      [ "$matches" -eq 1 ] || fail "$file: test \"$test\" matches $matches shard filters, must be 1"
    done < <(sed -nE 's/^@test "(.*)" \{$/\1/p' "$file")
  done < <(assigned_files)
}
check

case "${1:-}" in
  --list)
    printf '%s\n' "${!shards[@]}" rest | sort
    ;;
  --filter)
    echo "${filters[${2:-}]:-}"
    ;;
  rest)
    comm -23 <(all_files) <(assigned_files)
    ;;
  "")
    echo "usage: workflow-shards.sh <shard> | --filter <shard> | --list" >&2
    exit 2
    ;;
  *)
    [ -n "${shards[$1]:-}" ] || { echo "workflow-shards.sh: unknown shard $1" >&2; exit 2; }
    echo "${shards[$1]}"
    ;;
esac

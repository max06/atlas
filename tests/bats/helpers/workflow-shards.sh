#!/usr/bin/env bash
# Prints the bats files of one shard of the review workflow suites
# (tests/bats/workflow/ and redaction-replay.bats). CI runs each shard as its
# own job (test-actions.yml): the suites render a lot, and one runner needed
# about ten minutes for all of them.
#
#   workflow-shards.sh <shard>     the files of the shard, one per line
#   workflow-shards.sh --list      the shard names
#
# The shard "rest" is every file that no other shard names. A new test file
# runs there without a change to this script, so no file can drop out of CI.
# Balance the shards with the per-test times that `bats --timing` prints.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

declare -A shards=(
  [cli]="tests/bats/workflow/cli.bats
tests/bats/workflow/native-discover.bats
tests/bats/workflow/native-render.bats"
  [equivalence]="tests/bats/workflow/subset-equivalence.bats
tests/bats/workflow/classify.bats
tests/bats/workflow/scope-comment.bats"
  [actions]="tests/bats/workflow/review-action.bats
tests/bats/workflow/root-action.bats"
)

all_files() {
  find tests/bats/workflow -name '*.bats' | sort
  echo tests/bats/redaction/redaction-replay.bats
}

assigned_files() {
  local name
  for name in "${!shards[@]}"; do
    echo "${shards[$name]}"
  done | sort
}

# a shard must not name a file that is gone: it would hide a renamed suite
while read -r file; do
  [ -f "$file" ] || { echo "workflow-shards.sh: $file does not exist" >&2; exit 1; }
done < <(assigned_files)

case "${1:-}" in
  --list)
    printf '%s\n' "${!shards[@]}" rest | sort
    ;;
  rest)
    comm -23 <(all_files | sort) <(assigned_files)
    ;;
  "")
    echo "usage: workflow-shards.sh <shard>|--list" >&2
    exit 2
    ;;
  *)
    [ -n "${shards[$1]:-}" ] || { echo "workflow-shards.sh: unknown shard $1" >&2; exit 2; }
    echo "${shards[$1]}"
    ;;
esac

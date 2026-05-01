#!/usr/bin/env bash
#
# run-bats.sh — bats runner tailored for the TDD/iteration loop.
#
# Replaces the three-step ritual (rm temp dirs, run bats, count results)
# with one allowlistable invocation. Runs from any cwd inside the repo.
#
# Usage:
#   .claude/utils/run-bats.sh                       # full suite, summary
#   .claude/utils/run-bats.sh tests/bats/foo.bats   # single file, full output
#   .claude/utils/run-bats.sh -v                    # full suite, full output
#
# Exit code reflects test result: 0 if all tests passed, non-zero otherwise.

set -euo pipefail

# Always operate from the repo root so paths in the bats files line up
# regardless of where the caller invoked from.
cd "$(git rev-parse --show-toplevel)"

# Clean state from any previous (possibly interrupted) run. The bats
# helpers cache rendered output under these dirs; a stale cache from a
# different commit can mask real issues if setup_file expects empty
# state. Wildcards mean we can't safely allowlist the rm itself, but
# inside this script (which IS allowlisted) it runs unprompted.
rm -rf /tmp/bats-run-* /tmp/atlas-bats-render* /tmp/atlas-bats-sidedump* 2>/dev/null || true

case "${1-}" in
  -v|--verbose)
    exec bats tests/bats/
    ;;
  "")
    # Summary mode: capture the run, count, list failures only.
    log=$(mktemp)
    trap 'rm -f "$log"' EXIT
    bats tests/bats/ > "$log" 2>&1 || true
    ok=$(grep -c '^ok ' "$log" || true)
    notok=$(grep -c '^not ok' "$log" || true)
    echo "ok: $ok"
    echo "not ok: $notok"
    if [[ "$notok" -gt 0 ]]; then
      echo "--- failures ---"
      grep '^not ok' "$log" | head -30
      exit 1
    fi
    ;;
  *)
    # Single file or directory: full output for debugging.
    exec bats "$1"
    ;;
esac

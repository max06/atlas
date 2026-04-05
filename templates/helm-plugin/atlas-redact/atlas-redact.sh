#!/usr/bin/env bash
# ATLAS post-renderer: structurally replaces tainted values in rendered YAML.
#
# Receives rendered manifests on stdin, writes redacted manifests to stdout.
# The single argument is a base64-encoded JSON document {real: redacted}
# produced by atlas.diff.values inside templates/helmfile.single.yaml.gotmpl.
# We walk every scalar leaf in stdin; if its stringified form is a key in the
# map, we swap it for the corresponding redacted value. Parsing (via yq)
# rather than text-matching is what lets us handle multi-line block scalars,
# flow vs. block style, and types uniformly.
#
# Dependencies: yq (mikefarah/yq v4+), base64.

# (A) Dump what the plugin actually receives, from inside the plugin:
#     add this as the first line of atlas-redact.sh temporarily
echo "GOT ARG SIZE: ${#1}" >&2
printf '%s' "$1" | head -c 80 >&2; echo "..." >&2
printf '%s' "$1" | tail -c 80 >&2; echo "" >&2
# then look for odd characters or truncation

# (B) Check helmfile→helm arg handoff:
helmfile ... template --selector <yours> 2>&1 | head -40
# see if the stderr mentions the arg anywhere

set -euo pipefail

REPL_B64="${1:-}"
INPUT="$(cat)"

# No replacements → nothing to redact.
if [ -z "$REPL_B64" ]; then
  printf '%s' "$INPUT"
  exit 0
fi

# Bash's `$(...)` strips trailing newlines from captured output, which would
# break multi-line real values that end in a newline (e.g. PEM block scalars).
# Append a sentinel BEFORE capture and peel it off afterward so every byte
# of the decoded JSON survives intact.
REPL_TMP="$(printf '%s' "$REPL_B64" | base64 -d; printf X)"
REPL_JSON="${REPL_TMP%X}"

# Walk every scalar in the input and replace if a matching key exists in the
# map. Stringify the scalar for lookup (so int 42 matches a "42" key) — the
# redacted side already carries the right type shape from atlas.redact.value.
printf '%s' "$INPUT" | REPL_JSON="$REPL_JSON" yq eval-all '
  (strenv(REPL_JSON) | from_json) as $repl |
  (.. | select(tag == "!!str" or tag == "!!int" or tag == "!!float")
      | select(. as $v | $repl | has($v | tostring))
  ) |= ($repl[(. | tostring)])
'

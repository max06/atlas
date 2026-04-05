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

set -euo pipefail

REPL_B64="${1:-}"
INPUT="$(cat)"

# No replacements → nothing to redact.
if [ -z "$REPL_B64" ]; then
  printf '%s' "$INPUT"
  exit 0
fi

# Decode the map into a tempfile. yq's load() auto-detects the format via
# YAML parsing, which rejects some valid JSON payloads (it stumbles on
# certain escape-sequence combinations inside flow-style maps). Convert
# JSON → YAML upfront using yq's explicit JSON parser so load() below sees
# native YAML and parses cleanly regardless of content.
REPL_FILE="$(mktemp)"
trap 'rm -f "$REPL_FILE"' EXIT
printf '%s' "$REPL_B64" | base64 -d | yq -p json -o yaml > "$REPL_FILE"

# Walk every scalar in the input and replace if a matching key exists in the
# map. Stringify the scalar for lookup (so int 42 matches a "42" key) — the
# redacted side already carries the right type shape from atlas.redact.value.
printf '%s' "$INPUT" | yq eval-all '
  (load("'"$REPL_FILE"'")) as $repl |
  (.. | select(tag == "!!str" or tag == "!!int" or tag == "!!float")
      | select(. as $v | $repl | has($v | tostring))
  ) |= ($repl[(. | tostring)])
'

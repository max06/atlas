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
# Optional side-dump of the decoded replacement map. When the template wires
# a second argv (it only does so if ATLAS_SIDEDUMP_MAP_DIR was set at render
# time), we write the post-b64-decode map JSON to that path BEFORE scrubbing.
# The snapshot-review workflow uses the captured map to post-scrub a baseline
# render produced by an older ATLAS version whose own redaction missed
# template-level SOPS values. Path creation is best-effort (mkdir -p); a
# failed dump must NEVER block the render, so we warn on stderr and carry on.
MAP_DUMP_PATH="${2:-}"
INPUT="$(cat)"

# Empty input (e.g. a CRDs-only chart whose templates live under crds/ and
# are skipped by `helm template`) — pass through as an empty-but-non-zero
# document. helm's post-renderer contract rejects zero-byte stdout as
# "produced empty output", so we emit a single comment line to satisfy it.
if [ -z "$INPUT" ]; then
  echo "# atlas-redact: input stream was empty (nothing to redact)"
  exit 0
fi

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
if ! printf '%s' "$REPL_B64" | base64 -d | yq -p json -o yaml > "$REPL_FILE"; then
  echo "atlas-redact: failed to decode replacement map (b64 → JSON → YAML)" >&2
  exit 1
fi
if [ ! -s "$REPL_FILE" ]; then
  echo "atlas-redact: replacement map decoded to empty file" >&2
  exit 1
fi

# Side-dump the decoded map as JSON for the snapshot-review post-pass.
# Written AFTER the normal decode path succeeds, so a malformed map is
# caught by the check above rather than producing a corrupt dump file.
if [ -n "$MAP_DUMP_PATH" ]; then
  MAP_DUMP_DIR="$(dirname "$MAP_DUMP_PATH")"
  if mkdir -p "$MAP_DUMP_DIR" 2>/dev/null \
     && printf '%s' "$REPL_B64" | base64 -d > "$MAP_DUMP_PATH" 2>/dev/null; then
    :
  else
    echo "atlas-redact: side-dump to $MAP_DUMP_PATH failed (non-fatal)" >&2
  fi
fi

# Walk every scalar in the input and replace if a matching key exists in the
# map. Stringify the scalar for lookup (so int 42 matches a "42" key) — the
# redacted side already carries the right type shape from atlas.redact.value.
#
# We use `eval` (not `eval-all`) so each YAML document in the multi-doc
# stream is processed independently. `eval-all` merges all documents into
# one context; combined with the recursive `..` descent, that causes cross-
# document leakage and output explosion on larger manifests. The replacement
# map is loaded via strenv() → from_yaml inline — no load(), no eval-all.
REPL_YAML="$(cat "$REPL_FILE")"
OUTPUT="$(printf '%s' "$INPUT" | REPL="$REPL_YAML" yq eval '
  (strenv(REPL) | from_yaml) as $repl |
  (.. | select(tag == "!!str" or tag == "!!int" or tag == "!!float")
      | select(. as $v | $repl | has($v | tostring))
  ) |= ($repl[(. | tostring)])
')" || {
  echo "atlas-redact: yq eval failed on input stream" >&2
  exit 1
}

if [ -z "$OUTPUT" ]; then
  echo "atlas-redact: yq produced empty output" >&2
  echo "atlas-redact: input size was ${#INPUT} bytes, map file $(wc -c < "$REPL_FILE") bytes" >&2
  exit 1
fi

printf '%s\n' "$OUTPUT"

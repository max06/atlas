#!/usr/bin/env bash
# ATLAS snapshot-review post-pass: replay a captured redaction map over a
# rendered manifest tree produced by an older ATLAS version.
#
# Why this exists:
#   ATLAS versions before the "Split rendering" commit only built the
#   redaction map from hierarchy-level SOPS values — template-level SOPS
#   (release[].values: or release[].secrets:) slipped through and could
#   leak into the rendered output. We can't retroactively patch released
#   versions, so the snapshot-review workflow captures the current-version
#   map (via the atlas-redact plugin's side-dump mode) and replays it over
#   the older render here, closing the gap without a re-release.
#
# Inputs:
#   $1  BASELINE_ROOT — directory tree produced by `helmfile template
#       --output-dir <ROOT> --output-dir-template '{{.OutputDir}}/{{...cluster}}/{{...deploymentName}}/{{.Release.Name}}'`
#       so release dirs live at BASELINE_ROOT/<cluster>/<deployment>/<release>/.
#       Cluster may itself contain a slash (grouped cluster path).
#   $2  MAP_DIR — directory populated by the plugin's side-dump, same
#       layout: MAP_DIR/<cluster>/<deployment>/<release>.json.
#
# Output:
#   - Scrubbed YAML files in-place under BASELINE_ROOT.
#   - On stdout, one line per release: "<status> <release_path>" where
#     status is one of: scrubbed, no-map, no-yaml. The workflow consumes
#     these lines to bucket releases into both/added/removed (no-map =
#     baseline-only, dangerous; scrubbed = both, safe to diff).
#
# Dependencies: yq (mikefarah/yq v4+), base64.

set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <baseline-root> <map-dir>" >&2
  exit 2
fi

BASELINE_ROOT="$1"
MAP_DIR="$2"

if [ ! -d "$BASELINE_ROOT" ]; then
  echo "scrub-baseline: baseline root not a directory: $BASELINE_ROOT" >&2
  exit 2
fi

# Find release directories. A release dir is one containing a chart-name
# subdir with a "templates" directory underneath (helm template's output
# shape). We match on "templates" to tolerate any chart name and to work
# regardless of whether patches renamed the output file.
# Strip the trailing "/<chart>/templates" segment to get the release path
# relative to BASELINE_ROOT.
while IFS= read -r templates_dir; do
  [ -z "$templates_dir" ] && continue
  # templates_dir = BASELINE_ROOT/<cluster…>/<deployment>/<release>/<chart>/templates
  # Drop the /<chart>/templates suffix to get the release dir.
  release_dir="$(dirname "$(dirname "$templates_dir")")"
  # Release path relative to BASELINE_ROOT = <cluster…>/<deployment>/<release>.
  release_path="${release_dir#"$BASELINE_ROOT"/}"

  map_file="$MAP_DIR/${release_path}.json"
  if [ ! -f "$map_file" ]; then
    # No matching map. Older ATLAS rendered a release the current one
    # doesn't know about (removed release, renamed cluster, filter drift).
    # We cannot safely redact — emit a status line and leave the files
    # untouched. The workflow will drop the release from the diff to
    # avoid leaking through the unredacted baseline.
    echo "no-map $release_path"
    continue
  fi

  # Find YAML files to scrub under this release directory. Globbing with
  # -name '*.yaml' matches both plain and patched manifest filenames.
  yaml_files=()
  while IFS= read -r yf; do
    yaml_files+=("$yf")
  done < <(find "$release_dir" -type f -name '*.yaml')
  if [ "${#yaml_files[@]}" -eq 0 ]; then
    echo "no-yaml $release_path"
    continue
  fi

  # Encode the map as base64 so we can pass it through the same yq
  # pipeline atlas-redact.sh uses. Keep the expression identical: scrub
  # works via whole-scalar match, nothing else.
  map_b64="$(base64 -w0 < "$map_file" 2>/dev/null || base64 < "$map_file" | tr -d '\n')"
  # Decode back to YAML for the inline from_yaml call (same dance as the
  # plugin — some JSON escape sequences confuse yq's yaml auto-detect).
  repl_yaml="$(printf '%s' "$map_b64" | base64 -d | yq -p json -o yaml)"
  if [ -z "$repl_yaml" ]; then
    echo "scrub-baseline: empty map after decode for $release_path — skipping" >&2
    echo "no-map $release_path"
    continue
  fi

  for yf in "${yaml_files[@]}"; do
    # Pass 1 — structural Secret redaction, BEFORE the map replay, exactly
    # mirroring atlas-redact.sh (keep the two in sync): every v1/Secret's
    # data/stringData value becomes "REDACTED:sha256:<12-hex-of-value>".
    # Hashing the REAL baseline value makes the marker line up with the
    # current-side render — unchanged Secrets vanish from the diff, rotated
    # ones show a marker change. Running the map replay first would hash
    # already-redacted shapes instead and break that alignment.
    secret_vals="$(yq eval -N '
      [ select(.kind == "Secret" and .apiVersion == "v1")
        | (.data[]?, .stringData[]?)
        | select(tag != "!!null")
        | tostring | @base64 ]
      | .[]' "$yf" 2>/dev/null | sort -u)" || secret_vals=""
    if [ -n "$secret_vals" ]; then
      secret_map_json="$(while IFS= read -r b64; do
          [ -z "$b64" ] && continue
          hash="$(printf '%s' "$b64" | base64 -d | sha256sum | head -c 12)"
          printf '%s\t%s\n' "$b64" "$hash"
        done <<< "$secret_vals" | jq -Rn '
          reduce inputs as $line ({};
            ($line | split("\t")) as [$b64, $h]
            | . + {($b64 | @base64d): ("REDACTED:sha256:" + $h)})')"
      secret_map_yaml="$(printf '%s' "$secret_map_json" | yq -p json -o yaml)"
      tmp_out="$(mktemp)"
      if SECRET_REPL="$secret_map_yaml" yq eval '
        (strenv(SECRET_REPL) | from_yaml) as $m |
        (select(.kind == "Secret" and .apiVersion == "v1")
          | (.data[]?, .stringData[]?)
          | select(tag != "!!null")
        ) |= ($m[(. | tostring)] // "REDACTED:sha256:unmapped")
      ' "$yf" > "$tmp_out" 2>/dev/null; then
        mv "$tmp_out" "$yf"
      else
        rm -f "$tmp_out"
        echo "scrub-baseline: structural Secret pass failed on $yf — left unchanged" >&2
      fi
    fi

    # Pass 2 — replacement-map replay. Write to a tempfile then move —
    # never truncate the source until yq succeeded, so a scrub error
    # leaves the baseline untouched rather than half-written.
    tmp_out="$(mktemp)"
    if REPL="$repl_yaml" yq eval '
      (strenv(REPL) | from_yaml) as $repl |
      (.. | select(tag == "!!str" or tag == "!!int" or tag == "!!float")
          | select(. as $v | $repl | has($v | tostring))
      ) |= ($repl[(. | tostring)])
    ' "$yf" > "$tmp_out" 2>/dev/null; then
      mv "$tmp_out" "$yf"
    else
      rm -f "$tmp_out"
      echo "scrub-baseline: yq eval failed on $yf — left unchanged" >&2
    fi
  done

  echo "scrubbed $release_path"
done < <(find "$BASELINE_ROOT" -type d -name templates | sort -u)

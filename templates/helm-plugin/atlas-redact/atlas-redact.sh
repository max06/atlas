#!/usr/bin/env bash
# ATLAS post-renderer: redacts tainted secret values in rendered YAML output.
#
# Receives rendered manifests on stdin, writes redacted manifests to stdout.
# Arguments are ||-delimited tainted values to redact.
#
# Redaction rules:
#   - Strings: always replaced with "REDACTED"
#   - Numbers >= 5 digits: replaced with "0"
#   - Numbers < 5 digits: kept (not enough entropy)
#   - Booleans (true/false): kept (50/50 odds)
#
# Replaces longest values first to avoid partial matches.

set -euo pipefail

TAINTED_VALUES="${1:-}"

# Read rendered YAML from stdin
INPUT=$(cat)

# If no tainted values, pass through unchanged
if [ -z "$TAINTED_VALUES" ]; then
  printf '%s\n' "$INPUT"
  exit 0
fi

# Split values by || delimiter into an array, deduplicate
IFS='|' read -ra RAW_VALUES <<< "$TAINTED_VALUES"
declare -A SEEN
CLEAN_VALUES=()
for val in "${RAW_VALUES[@]}"; do
  # Trim whitespace
  val="${val#"${val%%[![:space:]]*}"}"
  val="${val%"${val##*[![:space:]]}"}"
  [ -z "$val" ] && continue
  [ "${SEEN[$val]+_}" ] && continue
  SEEN[$val]=1
  CLEAN_VALUES+=("$val")
done

# Sort by length descending (longest first) to avoid partial replacements
IFS=$'\n' SORTED=($(for val in "${CLEAN_VALUES[@]}"; do
  echo "${#val} $val"
done | sort -t' ' -k1 -rn | sed 's/^[0-9]* //'))
unset IFS

# Build sed replacement expressions
SED_ARGS=()
for val in "${SORTED[@]}"; do
  # Skip booleans
  if [ "$val" = "true" ] || [ "$val" = "false" ]; then
    continue
  fi

  # Check if it's a number
  if [[ "$val" =~ ^-?[0-9]*\.?[0-9]+$ ]]; then
    # Count significant digits (strip minus, decimal point)
    DIGITS="${val//[-.]}"
    if [ "${#DIGITS}" -ge 5 ]; then
      # Redact large numbers
      ESCAPED=$(printf '%s\n' "$val" | sed 's/[&/\\.^$*+?()[\]{}|]/\\&/g')
      SED_ARGS+=(-e "s/${ESCAPED}/0/g")
    fi
    # Small numbers: skip (keep as-is)
    continue
  fi

  # String value: skip very short strings (< 4 chars) which are unlikely to be
  # meaningful secrets and could cause false positives in replacements.
  if [ "${#val}" -lt 4 ]; then
    continue
  fi
  ESCAPED=$(printf '%s\n' "$val" | sed 's/[&/\\.^$*+?()[\]{}|]/\\&/g')
  # Only replace complete YAML values — match the tainted value when it appears
  # as the entire value after ": " (end of line) or as a list item after "- ".
  # This prevents substring matches in keys, paths, or longer values.
  SED_ARGS+=(-e "s/\(: \)${ESCAPED}$/\1REDACTED/g")
  SED_ARGS+=(-e "s/\(- \)${ESCAPED}$/\1REDACTED/g")
  # Also handle quoted values (single and double quotes)
  SED_ARGS+=(-e "s/\(: '\)${ESCAPED}'/\1REDACTED'/g")
  SED_ARGS+=(-e "s/\(: \"\)${ESCAPED}\"/\1REDACTED\"/g")
done

# Apply all replacements
if [ ${#SED_ARGS[@]} -gt 0 ]; then
  printf '%s\n' "$INPUT" | sed "${SED_ARGS[@]}"
else
  printf '%s\n' "$INPUT"
fi

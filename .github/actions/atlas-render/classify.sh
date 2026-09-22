#!/usr/bin/env bash
# ATLAS snapshot-review: classify a PR's changed paths into the render subset.
#
# The ATLAS directory convention IS the dependency graph, so changed paths map
# mechanically onto the (cluster, deployment) pairs whose render can differ.
# Given the discovery maps of both revisions (see discover.sh) and the list of
# changed paths, this script selects those pairs — or decides on a full render.
#
# Rules (per changed path; the selected sets are unioned, any "full" wins):
#
#   entry helmfile                          full   (the pipeline itself changed)
#   <templates>/<t>/**                      pairs instantiating template <t>; <t> is
#                                           matched against the template names the maps
#                                           know, so it may span directories
#                                           (templates/group/app/x → template group/app)
#   <templates>/<file>                      full   (a file directly in the templates root)
#   <deployments>/<prefix>/apps/<name>/**   deployment <name> on every leaf cluster
#                                           under <prefix> ("" = every cluster)
#   <deployments>/<prefix>/<file>           every deployment of every cluster under
#                                           <prefix>; <prefix> == "" → full (global values)
#   anything else                           full   (default-deny: charts/, docs, unknown)
#
# plus: pairs that exist on only ONE revision are always selected — a new
# cluster directory picks up group/global deployments no changed path points
# at, and a removed deployment must show as removed.
#
# Selection is deliberately over-approximate (a group-level change selects a
# cluster that shadows it with its own copy). Over-selection costs render
# time; under-selection is a silent green review, so every rule errs wide.
# Both sides then render the SAME selection intersected with their own map:
# a pair absent on one side simply does not render there (→ new/removed).
#
# Required environment:
#   CLASSIFY_OUT      — output directory
#   MAP_BASELINE      — discovery map JSON of the target branch (may be missing)
#   MAP_PR            — discovery map JSON of the merge result   (may be missing)
#   HELMFILE_PATH     — repo-relative path of the helmfile entry point
#   CHANGES_FILE      — `git diff --name-status --no-renames <base> <head>` output
#                       (one "<status>\t<path>" line per change)
#
# Optional environment:
#   GITHUB_OUTPUT     — step outputs (default: /dev/null)
#
# Outputs (GITHUB_OUTPUT):
#   mode               — subset / full
#   reason             — short reason when mode=full
#   selected_baseline / total_baseline — pairs selected / present on the baseline
#   selected_pr       / total_pr       — same for the PR side
#   pairs_baseline_file / pairs_pr_file — "<cluster>|<deployment>" line files for render.sh
#
# Output files ($CLASSIFY_OUT):
#   classify.json        — full decision record (per-path rules, selected pairs)
#   pairs-baseline.txt   — selected pairs present on the baseline
#   pairs-pr.txt         — selected pairs present on the PR side
#   classify-summary.md  — markdown fragment for the review comment
set -euo pipefail

CLASSIFY_OUT="${CLASSIFY_OUT:?CLASSIFY_OUT is required}"
MAP_BASELINE="${MAP_BASELINE:?MAP_BASELINE is required}"
MAP_PR="${MAP_PR:?MAP_PR is required}"
HELMFILE_PATH="${HELMFILE_PATH:?HELMFILE_PATH is required}"
CHANGES_FILE="${CHANGES_FILE:?CHANGES_FILE is required}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

mkdir -p "$CLASSIFY_OUT"

# Missing map on either side = that ATLAS cannot describe itself → full render.
read_map() { if [ -s "$1" ]; then jq -c . "$1"; else echo null; fi; }
BASE_MAP="$(read_map "$MAP_BASELINE")"
PR_MAP="$(read_map "$MAP_PR")"

# Changed paths → JSON [{status, path}]. `--no-renames` keeps this one path
# per line (a rename is a D and an A). Paths are repo-relative, like the
# map's deploymentPath and the helmfile entry.
CHANGES_JSON="$(awk -F'\t' 'NF>=2 {printf "%s\t%s\n", $1, $2}' "$CHANGES_FILE" \
  | jq -R -s -c 'split("\n") | map(select(length>0) | split("\t") | {status: .[0], path: .[1]})')"

jq -n -c \
  --argjson base "$BASE_MAP" \
  --argjson pr "$PR_MAP" \
  --argjson changes "$CHANGES_JSON" \
  --arg entry "$HELMFILE_PATH" '
  # ── helpers ──────────────────────────────────────────────────────────────
  def key: .cluster + "|" + .deploymentName;
  def under($root): ($root == "") or (. == $root) or startswith($root + "/");
  def strip_root($root): if $root == "" then . else .[($root | length) + 1:] end;
  def cluster_under($prefix): ($prefix == "") or (. == $prefix) or startswith($prefix + "/");
  def full($reason): {full: true, reason: $reason, pairs: []};
  def select_pairs($rule; $detail; $pairs):
    {full: false, rule: $rule, detail: $detail, pairs: ($pairs | map(key))};

  # ── preconditions ────────────────────────────────────────────────────────
  if $base == null or $pr == null then
    {mode: "full", reason: (if $base == null and $pr == null then "no discovery map on either side"
                            elif $base == null then "no discovery map on the target branch"
                            else "no discovery map on the merge result" end),
     changes: $changes, selected: [], base_total: 0, pr_total: 0}
  elif $base.deploymentsRoot != $pr.deploymentsRoot or $base.templatesRoot != $pr.templatesRoot then
    {mode: "full", reason: "deployments/templates root changed between revisions",
     changes: $changes, selected: [], base_total: ($base.pairs|length), pr_total: ($pr.pairs|length)}
  else
    # roots as configured by the consumer, normalized ("./deployments/" → "deployments")
    def norm_root: ltrimstr("./") | sub("/+$"; "");
    ($pr.deploymentsRoot | norm_root) as $D | ($pr.templatesRoot | norm_root) as $T |
    # union of both maps, templates merged per pair
    ([$base.pairs[], $pr.pairs[]] | group_by(key)
      | map({cluster: .[0].cluster, deploymentName: .[0].deploymentName,
             templates: ([.[].templates[]] | unique)})) as $all |
    ($base.pairs | map(key)) as $baseKeys | ($pr.pairs | map(key)) as $prKeys |
    (($baseKeys - $prKeys) + ($prKeys - $baseKeys)) as $onlyOneSide |

    # ── per-path classification ──────────────────────────────────────────
    ($changes | map(.path as $p |
      if $p == $entry then . + full("entry helmfile changed")
      elif ($p | under($T)) then
        ($p | strip_root($T)) as $rel | ($rel | split("/")) as $segs |
        if ($segs | length) < 2 then . + full("file directly in the templates root: " + $p)
        else
          # Template names may contain "/" (a template in a subdirectory), so the
          # name is not the first path segment: every known template whose
          # directory contains the path is selected. Nested templates
          # (templates/a and templates/a/b) both match a file under a/b — wide on
          # purpose. A path under no known template selects nothing, shown
          # under the first segment so the comment still names a directory.
          ([$all[].templates[]] | unique | map(select(. as $t | $rel | startswith($t + "/")))) as $hits |
          (if ($hits | length) > 0 then $hits else [$segs[0]] end) as $names |
          . + select_pairs("template"; ($names | join(", ")); [$all[] | select(.templates | any(. as $t | $names | index($t)))])
        end
      elif ($p | under($D)) then
        ($p | strip_root($D) | split("/")) as $segs |
        ($segs | index("apps")) as $i |
        if $i != null then
          ($segs[:$i] | join("/")) as $prefix | ($segs[$i+1] // "") as $name |
          if $name == "" or ($segs | length) < $i + 3 then
            # a file directly under apps/ is read by nothing
            . + select_pairs("apps-dir-file"; $p; [])
          else
            . + select_pairs("deployment"; (if $prefix == "" then "apps/" + $name else $prefix + "/apps/" + $name end);
                  [$all[] | select(.deploymentName == $name and (.cluster | cluster_under($prefix)))])
          end
        else
          ($segs[:-1] | join("/")) as $prefix |
          if $prefix == "" then . + full("global hierarchy file changed: " + $p)
          else . + select_pairs("hierarchy"; $prefix; [$all[] | select(.cluster | cluster_under($prefix))])
          end
        end
      else . + full("outside deployments/templates: " + $p)
      end)) as $classified |

    ([$classified[] | select(.full)] | first) as $firstFull |
    if $firstFull != null then
      {mode: "full", reason: $firstFull.reason, changes: $classified, selected: [],
       base_total: ($baseKeys|length), pr_total: ($prKeys|length)}
    else
      ([$classified[].pairs[]] + $onlyOneSide | unique) as $selected |
      {mode: "subset", reason: "",
       changes: $classified,
       one_side_only: $onlyOneSide,
       selected: $selected,
       selected_baseline: [$selected[] | select(. as $k | $baseKeys | index($k))],
       selected_pr:       [$selected[] | select(. as $k | $prKeys   | index($k))],
       base_total: ($baseKeys|length), pr_total: ($prKeys|length)}
    end
  end' > "$CLASSIFY_OUT/classify.json"

MODE="$(jq -r .mode "$CLASSIFY_OUT/classify.json")"
REASON="$(jq -r .reason "$CLASSIFY_OUT/classify.json")"
BASE_TOTAL="$(jq -r .base_total "$CLASSIFY_OUT/classify.json")"
PR_TOTAL="$(jq -r .pr_total "$CLASSIFY_OUT/classify.json")"

: > "$CLASSIFY_OUT/pairs-baseline.txt"
: > "$CLASSIFY_OUT/pairs-pr.txt"
SEL_BASE=0; SEL_PR=0
if [ "$MODE" = "subset" ]; then
  jq -r '.selected_baseline[]' "$CLASSIFY_OUT/classify.json" > "$CLASSIFY_OUT/pairs-baseline.txt"
  jq -r '.selected_pr[]'       "$CLASSIFY_OUT/classify.json" > "$CLASSIFY_OUT/pairs-pr.txt"
  SEL_BASE="$(jq '.selected_baseline | length' "$CLASSIFY_OUT/classify.json")"
  SEL_PR="$(jq '.selected_pr | length' "$CLASSIFY_OUT/classify.json")"
fi

# ── Comment fragment ────────────────────────────────────────────────────────
{
  if [ "$MODE" = "subset" ]; then
    echo "**Render scope:** ${SEL_PR} of ${PR_TOTAL} deployments (merge result), ${SEL_BASE} of ${BASE_TOTAL} (target branch) — selected from the changed paths."
    echo
    echo "<details>"
    echo "<summary>Selection rules applied</summary>"
    echo
    echo "| Changed path | Rule | Selected |"
    echo "|---|---|---|"
    jq -r '.changes[] | "| `\(.path)` | \(.rule) \(if .detail != "" then "`" + .detail + "`" else "" end) | \(.pairs | length) |"' "$CLASSIFY_OUT/classify.json"
    ONE_SIDE="$(jq -r '.one_side_only | length' "$CLASSIFY_OUT/classify.json")"
    if [ "$ONE_SIDE" != "0" ]; then
      echo "| _(pairs present on one revision only)_ | added/removed | ${ONE_SIDE} |"
    fi
    echo
    echo "</details>"
  else
    echo "**Render scope:** full render — ${REASON}."
  fi
} > "$CLASSIFY_OUT/classify-summary.md"

{
  echo "mode=$MODE"
  echo "reason=$REASON"
  echo "selected_baseline=$SEL_BASE"
  echo "total_baseline=$BASE_TOTAL"
  echo "selected_pr=$SEL_PR"
  echo "total_pr=$PR_TOTAL"
  echo "pairs_baseline_file=$CLASSIFY_OUT/pairs-baseline.txt"
  echo "pairs_pr_file=$CLASSIFY_OUT/pairs-pr.txt"
} >> "$GITHUB_OUTPUT"

echo "classify: mode=$MODE${REASON:+ ($REASON)} pr=${SEL_PR}/${PR_TOTAL} baseline=${SEL_BASE}/${BASE_TOTAL}"

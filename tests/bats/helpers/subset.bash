# subset.bash — helpers for the changed-file render-subsetting tests.
#
# The subsetting pipeline (discover.sh → classify.sh → render.sh with a pairs
# file) reasons about TWO revisions of a consumer repo. These helpers build a
# standalone git repo from the ATLAS test fixtures, so a test can mutate the
# tree, commit, and run the pipeline against HEAD~1 (baseline) and HEAD (PR).
#
# Layout of the fixture repo (mirrors a consumer repo):
#   <repo>/helmfile.yaml.gotmpl   entry → this checkout's ATLAS root
#   <repo>/deployments            copy of tests/deployments
#   <repo>/templates              copy of tests/templates
#   <repo>/charts                 copy of tests/charts (templates reference
#                                 ../../charts/chart1 and {{ atlas.cwd }}/charts)

# bats `load` resolves relative to the TEST file, so a helper sources its
# sibling explicitly.
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/render.bash"

_actions_dir() { echo "$(_repo_root)/.github/actions"; }

# make_fixture_repo <dir> — create the repo with one commit ("base").
make_fixture_repo() {
  local dir="$1" root
  root="$(_repo_root)"
  mkdir -p "$dir"
  cp -r "$root/tests/deployments" "$root/tests/templates" "$root/tests/charts" "$dir/"
  cat > "$dir/helmfile.yaml.gotmpl" <<EOT
helmfiles:
  - path: ${root}/helmfile.yaml.gotmpl
    values:
      - atlas:
          cwd: {{ exec "pwd" (list) | trim }}
          appTemplates: templates
          deploymentDefinitions: deployments
EOT
  git -C "$dir" init -q -b main
  fixture_commit "$dir" "base"
}

# fixture_commit <dir> <message> — stage everything and commit (unsigned, fixed identity).
fixture_commit() {
  git -C "$1" add -A
  git -C "$1" -c user.email=atlas-tests@localhost -c user.name="atlas tests" \
    -c commit.gpgsign=false commit -q --allow-empty -m "$2"
}

# export_rev <dir> <rev> <dest> — materialize a revision's tree (git archive).
export_rev() {
  mkdir -p "$3"
  git -C "$1" archive "$2" | tar -x -C "$3"
}

# discovery_map_of <tree> <out.json> — run discover.sh inside a tree.
discovery_map_of() {
  ( cd "$1" && HELMFILE_PATH=helmfile.yaml.gotmpl "$(_actions_dir)/atlas-render/discover.sh" "$2" >/dev/null )
}

# changes_between <dir> <base> <head> <out.txt> — name-status list, no renames.
changes_between() {
  git -C "$1" diff --name-status --no-renames "$2" "$3" > "$4"
}

# classify_revisions <dir> <base> <head> <out-dir>
# Exports both trees, emits both maps, diffs, and runs classify.sh. The
# GITHUB_OUTPUT file lands in <out-dir>/github-output. Trees and maps are
# cached per (dir, rev) under <out-dir>/.. so several scenarios sharing the
# same base commit pay for its map once.
classify_revisions() {
  local dir="$1" base="$2" head="$3" out="$4" cache
  cache="$(dirname "$out")/.rev-cache"
  mkdir -p "$out" "$cache"
  local base_sha head_sha
  base_sha="$(git -C "$dir" rev-parse "$base")"
  head_sha="$(git -C "$dir" rev-parse "$head")"
  for sha in "$base_sha" "$head_sha"; do
    if [ ! -f "$cache/$sha.map.json" ]; then
      export_rev "$dir" "$sha" "$cache/$sha"
      discovery_map_of "$cache/$sha" "$cache/$sha.map.json"
    fi
  done
  changes_between "$dir" "$base_sha" "$head_sha" "$out/changes.txt"
  CLASSIFY_OUT="$out" \
  MAP_BASELINE="$cache/$base_sha.map.json" \
  MAP_PR="$cache/$head_sha.map.json" \
  HELMFILE_PATH=helmfile.yaml.gotmpl \
  CHANGES_FILE="$out/changes.txt" \
  GITHUB_OUTPUT="$out/github-output" \
    "$(_actions_dir)/atlas-render/classify.sh" > "$out/classify.log"
  # convenience copies for assertions
  cp "$cache/$base_sha.map.json" "$out/map-baseline.json"
  cp "$cache/$head_sha.map.json" "$out/map-pr.json"
  # ATLAS_CLI=<atlas binary>: every scenario doubles as an equivalence check
  # of the Go port (`atlas review classify`) against classify.sh.
  if [ -n "${ATLAS_CLI:-}" ]; then
    cli_matches_classify_sh "$out" "$cache/$base_sha.map.json" "$cache/$head_sha.map.json"
  fi
}

# cli_matches_classify_sh <out-dir> <map-base> <map-pr> — run the CLI port on
# classify.sh's inputs and compare every output (JSON key order aside).
cli_matches_classify_sh() {
  local out="$1" cli="$1/cli"
  "$ATLAS_CLI" review classify -f helmfile.yaml.gotmpl \
    --map-base "$2" --map-head "$3" --changes-file "$out/changes.txt" \
    --out "$cli" --github-output "$cli/github-output" >/dev/null 2>"$cli.log" \
    || { echo "atlas review classify failed:"; cat "$cli.log"; return 1; }
  diff <(jq -S . "$out/classify.json") <(jq -S . "$cli/classify.json") \
    || { echo "classify.json differs (classify.sh vs CLI)"; return 1; }
  local f
  for f in pairs-baseline.txt pairs-pr.txt classify-summary.md; do
    diff "$out/$f" "$cli/$f" || { echo "$f differs (classify.sh vs CLI)"; return 1; }
  done
  diff <(grep -v '_file=' "$out/github-output") <(grep -v '_file=' "$cli/github-output") \
    || { echo "step outputs differ (classify.sh vs CLI)"; return 1; }
}

# pairs_where <map.json> <jq-select-expr> — "<cluster>|<deployment>" lines, sorted.
pairs_where() {
  jq -r ".pairs[] | select($2) | .cluster + \"|\" + .deploymentName" "$1" | sort
}

# sorted_file <file> — sorted content (for set comparison).
sorted_file() { sort "$1"; }

# gh_output <out-dir> <key>
gh_output() { grep "^${2}=" "$1/github-output" | head -1 | cut -d= -f2-; }

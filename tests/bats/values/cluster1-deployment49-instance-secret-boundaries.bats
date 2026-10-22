#!/usr/bin/env bats
#
# Scenario: the secret boundaries of the instance layer (max06/atlas#86).
#
#   template values < template secrets < hierarchy < instance inline < instance secrets
#
# Fixture (deployment49 / app-secrets, SOPS files shared with deployment38):
#   key1 — only in template values                         -> fromValues
#   key2 — template values + template secrets + instance inline
#                                                          -> fromInstanceInline
#   key3 — template secrets + instance inline + instance secrets
#                                                          -> fromInstanceSecret

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment49
RELEASE=stage3-secrets-release

setup_file() { ensure_rendered; }

@test "d49: release rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d49: key1 from template values survives" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key1
  [ "$output" = "fromValues" ]
}

@test "d49: key2 — instance inline beats a template secret" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key2
  [ "$output" = "fromInstanceInline" ]
}

@test "d49: key3 — instance secret beats instance inline" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key3
  [ "$output" = "fromInstanceSecret" ]
}

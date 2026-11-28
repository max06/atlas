# Development environment for ATLAS.
#
# Provides the toolchain the bats suites and the Go code need, matching the
# CI workflows (.github/workflows/tests.yml, test-actions.yml):
#
#   nix develop                                   # interactive shell
#   nix develop --command .claude/utils/run-bats.sh   # one-shot, non-interactive
#
# The one-shot form is what non-interactive callers (scripts, Claude Code)
# use: every invocation gets the same environment without entering a shell.
#
# Version floors from CLAUDE.md: helm >= 4.0.0, helmfile >= 1.0.0.
{
  description = "ATLAS development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # helmfile.yaml.gotmpl enforces helmfile >= 1.8.1 (the oldest version
      # CI tests). Pin it here while nixpkgs still ships an older release;
      # drop the override once nixpkgs' helmfile reaches the floor.
      helmfileFor = pkgs:
        if pkgs.lib.versionAtLeast pkgs.helmfile.version "1.8.1" then pkgs.helmfile
        else pkgs.helmfile.overrideAttrs (old: rec {
          version = "1.8.1";
          src = pkgs.fetchFromGitHub {
            owner = "helmfile";
            repo = "helmfile";
            rev = "v${version}";
            hash = "sha256-G9gOc7gIj2AoROPRppHqkeGcVUOpYx/qaEVkec4c4rk=";
          };
          vendorHash = "sha256-GBTUYoXCcRD6kZI52VWE46GDlO13ZpxDEYrDd7nRR3s=";
          ldflags = map
            (flag: builtins.replaceStrings [ old.version ] [ version ] flag)
            (old.ldflags or [ ]);
        });
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            # Render toolchain
            kubernetes-helm
            (helmfileFor pkgs)
            # helmfile shells out to kustomize for release `strategicMergePatches`
            kustomize

            # Secrets (SOPS fixtures are encrypted with the age key in tests/)
            sops
            age

            # Test runner and the tools the suites shell out to
            bats
            parallel
            yq-go
            jq
            dyff
            git
            diffutils

            # atlas CLI / native engine (go.mod pins the language version)
            go_1_27
          ];

          shellHook = ''
            repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

            # Same settings the CI jobs export.
            export SOPS_AGE_KEY_FILE="$repo_root/tests/sops-secret.txt"
            # helmfile's update notice phones home on every invocation.
            export HELMFILE_UPGRADE_NOTICE_DISABLED=1

            # Use the Go from this shell, never a downloaded toolchain.
            export GOTOOLCHAIN=local

            # mkShell exports the derivation's `out` path. Bash carries the
            # export flag over to any `local out` in a function, so the bats
            # helpers that keep a large render in `local out` would hand it
            # to every child process as an environment variable and hit the
            # kernel's 128 KiB per-variable limit ("Argument list too long").
            unset out
          '';
        };
      });
    };
}

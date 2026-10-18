#!/usr/bin/env bash
# Test shim: the render.sh contract served by the atlas CLI (ATLAS_REVIEW_BACKEND=cli).
exec "${ATLAS_CLI:?ATLAS_CLI is required}" review ci render "$@"

#!/usr/bin/env bash
# Runs the pinned golangci-lint over the workspace with the rules in
# .golangci.yml. It reads the module cache and type-checks, so it runs from the
# workspace rather than in a sandbox.
set -euo pipefail

tool="$(realpath "$1")"
shift
cd "${BUILD_WORKSPACE_DIRECTORY:?run this with bazel run //:lint}"

exec "$tool" run ./... "$@"

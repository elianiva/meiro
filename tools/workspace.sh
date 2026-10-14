#!/usr/bin/env bash
# Runs a command from the workspace root, so a bazel run target drives the
# repository's own tooling with the paths a person would use.
set -euo pipefail

cd "${BUILD_WORKSPACE_DIRECTORY:?this target is meant to be run with bazel run}"
exec "$@"

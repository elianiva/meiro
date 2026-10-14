#!/usr/bin/env bash
# Removes build output and the downloaded tools. Bazel's own output is
# untouched: `bazel clean` is the command for that.
set -euo pipefail

cd "${BUILD_WORKSPACE_DIRECTORY:?run this with bazel run //:clean}"
rm -rf build .mygo resources/darwin-* resources/linux-* resources/windows-*

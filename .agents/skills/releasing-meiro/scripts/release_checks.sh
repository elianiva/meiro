#!/usr/bin/env bash
# The local checks a release needs before pushing: what CI does not cover
# (the release-preparation integrity check), a quick host compile, and lint
# as fast feedback because CI's lint download can fail upstream. CI runs the
# race tests, format check, cross-compiles and lint on the pushed commit.
set -euo pipefail
version=${1:?usage: release_checks.sh VERSION}
root=$(git rev-parse --show-toplevel)
cd "$root"

echo "=== RUN prepare_release.py $version --verify"
PYTHONDONTWRITEBYTECODE=1 python3 -B .agents/skills/releasing-meiro/scripts/prepare_release.py "$version" --verify
exec .agents/skills/verify-meiro/scripts/checks.sh build lint

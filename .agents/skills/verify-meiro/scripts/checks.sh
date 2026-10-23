#!/usr/bin/env bash
# Run repository checks one phase at a time, streaming the active phase's log,
# then print one summary with the tail of each failed phase.
#
#   checks.sh [STEP...]
#
# Steps: test (race tests, which also cover the plain tests), lint, format,
# build (host compile), gazelle. With no steps it runs: test lint format.
set -uo pipefail
root=$(git rev-parse --show-toplevel)
cd "$root" || exit 2

steps=("$@")
[ "${#steps[@]}" -gt 0 ] || steps=(test lint format)
for step in "${steps[@]}"; do
	case $step in
	test | lint | format | build | gazelle) ;;
	*)
		echo "usage: checks.sh [test|lint|format|build|gazelle]..." >&2
		exit 2
		;;
	esac
done

logs=$(mktemp -d)
trap 'rm -rf "$logs"' EXIT
names=()
statuses=()

# Run one phase in the background and follow its log until it exits, so the
# caller sees continuous progress instead of one header line and silence.
run() {
	local name=$1
	shift
	local log="$logs/${#names[@]}.log"
	printf '\n=== RUN %s\n' "$name"
	local start=$SECONDS
	: >"$log"
	"$@" >>"$log" 2>&1 &
	local pid=$!
	tail -n +1 -f --pid="$pid" "$log"
	wait "$pid"
	local status=$?
	names+=("$name")
	if [ "$status" -eq 0 ]; then
		statuses+=("PASS ($((SECONDS - start))s)")
	else
		statuses+=("FAIL($status) ($((SECONDS - start))s)")
		cp "$log" "$logs/failure-${#names[@]}.log"
	fi
}

if command -v bazel >/dev/null 2>&1; then
	for step in "${steps[@]}"; do
		case $step in
		test) run 'bazel test --config=race //...' bazel test --config=race //... ;;
		lint) run 'bazel run //:lint' bazel run //:lint ;;
		format) run 'bazel run //:format-check' bazel run //:format-check ;;
		build) run 'bazel build //...' bazel build //... ;;
		gazelle) run 'bazel run //:gazelle' bazel run //:gazelle ;;
		esac
	done
else
	echo 'NOTE  bazel unavailable; using go test/vet/build, format and gazelle skipped'
	for step in "${steps[@]}"; do
		case $step in
		test) run 'go test ./... -count=1' go test ./... -count=1 ;;
		lint) run 'go vet ./...' go vet ./... ;;
		build) run 'go build ./...' go build ./... ;;
		esac
	done
fi

printf '\nCHECK SUMMARY\n'
failed=0
for i in "${!names[@]}"; do
	printf '%-38s %s\n' "${names[$i]}" "${statuses[$i]}"
	[[ "${statuses[$i]}" == PASS* ]] || failed=1
done
if [ "$failed" -ne 0 ]; then
	for failure in "$logs"/failure-*.log; do
		[ -e "$failure" ] || continue
		printf '\n--- %s (last 60 lines) ---\n' "$(basename "$failure")"
		tail -n 60 "$failure"
	done
fi
exit "$failed"

---
name: releasing-meiro
description: Prepares and publishes Meiro desktop releases and checks out/reviews Meiro pull requests, including changelogs, version tags, GitHub releases, CI monitoring, and GitHub comments. Use when preparing or shipping a release or reviewing a Meiro PR.
---

# Releasing Meiro

Prepare and publish a Meiro release using the repository's version tags and GitHub Actions workflows.

## Prepare the release

1. The orb clone starts shallow; unshallow it first so tags and commit ranges
   resolve. Then confirm the working tree has no unrelated changes and read
   `CHANGELOG.md`, `mygo.json`, `MODULE.bazel`, and
   `.github/workflows/release.yml`:

   ```sh
   [ "$(git rev-parse --is-shallow-repository)" = false ] || git fetch --quiet --unshallow --tags origin
   ```

2. Review every commit since the previous `desktop-vX.Y.Z` tag. Add concise,
   user-facing notes for shipped changes under `## [Unreleased]` in
   `CHANGELOG.md`; do not list refactors or CI-only changes.
3. Test the release-preparation helper against its isolated fixture before
   using it on repository files. Python bytecode is disabled so the skill tree
   stays clean:

   ```sh
   PYTHONDONTWRITEBYTECODE=1 python3 -B .agents/skills/releasing-meiro/scripts/test_prepare_release.py
   ```

4. Dry-run on the repository and inspect the printed diff. The helper is
   read-only by default; pass the version without `v`. Use `--date YYYY-MM-DD`
   only when needed:

   ```sh
   PYTHONDONTWRITEBYTECODE=1 python3 -B .agents/skills/releasing-meiro/scripts/prepare_release.py 0.4.2
   ```

   Only after reviewing the diff, run it again with `--apply`. The helper
   validates all inputs before writing atomically, then verifies the changelog
   section and matching versions in `MODULE.bazel` and `mygo.json`:

   ```sh
   PYTHONDONTWRITEBYTECODE=1 python3 -B .agents/skills/releasing-meiro/scripts/prepare_release.py 0.4.2 --apply
   ```

5. Run the release checks once:

   ```sh
   .agents/skills/releasing-meiro/scripts/release_checks.sh 0.4.2
   ```

   It runs `prepare_release.py --verify`, a host `bazel build //...`, and
   `bazel run //:lint`, streaming each phase's log and ending with a summary.
   Lint runs locally because CI's lint download can fail upstream. Do not run
   the full `verify-meiro` `checks.sh` suite for a release: CI runs the race
   tests, format check, lint, and every cross-compile on the pushed commit.
   Do not rerun checks after a metadata-only amend; CI checks the final
   commit.

## Publish and monitor

Every wait in this flow is a GitHub CI subscription, not a polling loop. Do
not loop on `gh run list`/`gh run view`, `gh run watch`, or a sleep wrapper,
and do not fetch `gh run view --json jobs` mid-run (the payload is large
enough to be truncated). Amp delivers the run's result to this thread; end
the turn after subscribing and continue when it arrives.

1. Commit only the release version, changelog, and preparation tooling. Do not
   include unrelated changes. Commit first: rebasing with uncommitted changes
   fails with "cannot rebase: You have unstaged changes".
2. Update against the remote and rebase the commit; do not respond to a
   rejected push by force-pushing:

   ```sh
   git fetch origin master
   git rebase origin/master
   ```

   If `CHANGELOG.md` conflicts in `## [Unreleased]`, combine both sides by
   category, keep upstream bullets first and local bullets second, and remove
   only exact duplicate bullets. For a dated release section, keep upstream's
   published history authoritative and move any genuinely new notes to
   Unreleased. If release versions or section ordering conflict, stop and
   resolve the release intent before continuing; never choose ours/theirs for
   the entire file. Rerun `release_checks.sh` after resolving conflicts, then
   push the rebased commit to `master`.
3. Right after the push, subscribe to `CI` for the exact pushed SHA
   (`git rev-parse HEAD`). Find `amp.create_github_subscription` with
   `tool_search` and call it through `code_exec`; the subscription exists
   before GitHub lists the run, so no wait for the run to appear is needed:

   ```js
   import { create_github_subscription } from "amp"
   text(await create_github_subscription({
     title: "Meiro 0.4.2 CI", repository: "elianiva/meiro", filter_kind: "ci",
     commit_sha: "<full sha>", workflow_file: "ci.yml",
     automatic: true, branch: "master",
   }))
   ```

   If CI fails, read the failing job's log (`gh run view RUN_ID --log-failed`),
   fix the cause, commit, fetch/rebase, push, and subscribe again for the new
   SHA. Tag only after CI is green on the exact commit you will tag.
4. Create and push the full-build release tag on that commit:

   ```sh
   git tag desktop-v0.4.2
   git push origin desktop-v0.4.2
   ```

   The tag push starts `.github/workflows/release.yml`. It checks that the tag
   matches `mygo.json`, confirms CI passed on that commit, builds platform
   packages, and publishes the full release plus the minimal-update release.
   The workflow takes release notes from the version's `CHANGELOG.md` section.
   Tag pushes do not run `CI`.
5. Right after the tag push, subscribe to the release workflow for the same
   SHA. A tag is not a branch, so this is an explicit listener:

   ```js
   import { create_github_subscription } from "amp"
   text(await create_github_subscription({
     title: "Meiro 0.4.2 release", repository: "elianiva/meiro", filter_kind: "ci",
     commit_sha: "<full sha>", workflow_file: "release.yml", automatic: false,
   }))
   ```

   Investigate failures before retrying; a manual workflow run on the same
   tag is the documented retry path; once that rerun starts, create a new
   subscription for it, because a final CI result ends the existing one.
   When it succeeds, confirm both releases
   are published with assets:

   ```sh
   .agents/skills/releasing-meiro/scripts/verify_release.sh 0.4.2
   ```

6. Do not mark or unmark a release as GitHub's latest manually, and do not
   investigate which release `/releases/latest` returns. The workflow
   publishes with `--latest=false`, but that endpoint returns the newest
   non-draft, non-prerelease release regardless, so `desktop-vX.Y.Z` showing
   as latest is expected. Meiro's updater finds releases by the `desktop-v`
   tag prefix.

Do not force-push or move an existing release tag. If the tag already exists,
inspect its commit and release state before taking any action.

### `gh` JSON fields

Check fields against `gh <command> --help` before guessing. Known traps:

- `gh run list` has no `jobs` field (`gh run view` does).
- `gh release view` has no `isLatest` (`gh release list` does).
- `gh release list` has no `url` field; use `gh release view TAG --json url`.

## Pull requests and GitHub comments

When reviewing a PR, check it out in a separate worktree so tests run on the
actual PR contents rather than using CI as a proxy:

```sh
gh pr checkout 166 --worktree ../meiro-pr-166
cd ../meiro-pr-166
```

Run the relevant checks from that worktree. Remove it with `git worktree
remove ../meiro-pr-166` when finished; do not disturb the primary checkout.

Use the `gh` comment commands for ordinary PR/issue follow-up instead of
inventing API calls or searching for absent MCP write tools:

```sh
gh pr comment 166 --body-file comment.md
gh issue comment 123 --body-file comment.md
gh pr review 166 --comment --body-file review.md
```

Only post comments or reviews when the user explicitly asks.

# Contributing to Meiro

Thanks for your interest in contributing. You can help by fixing a bug,
improving an existing feature, writing tests or documentation, or reporting a
problem for a maintainer to investigate.

## Choose how to contribute

- **Found an issue and want to fix it?** Feel free to work on it and open a
  pull request. If there is an existing issue, link it in your pull request.
  If an issue groups several tasks, choose a specific task from its checklist.
- **Found a problem but do not want to implement a fix?** Open a detailed
  GitHub issue. Include the steps to reproduce it, what you expected, what
  happened, and your operating system and Meiro version. A maintainer can pick
  it up.
- **Considering a large change or a change to user-facing behavior?** Open or
  comment on an issue first. This helps avoid duplicate work and gives the
  maintainer a chance to flag constraints before you spend time on the change.
  You do not need approval before fixing a clear, contained bug.

When possible, comment on an issue before starting substantial work so others
know it is being handled. If someone else is already working on it, coordinate
before duplicating the effort.

## Set up the project

Install [Bazel](https://bazel.build/) through
[bazelisk](https://github.com/bazelbuild/bazelisk), which reads the pinned
version from [`.bazelversion`](.bazelversion); Bazel downloads the Go toolchain
listed in [`go.mod`](go.mod) itself. For local development, `ffmpeg` and
`yt-dlp` must be available on `PATH`.

```sh
bazel run //:dev
```

`bazel run //:clean` removes the build output and the downloaded tools. Before
opening a pull request, run the tests:

```sh
bazel test --config=race //...
```

You can run a focused test while working, for example
`bazel test //youtube:youtube_test`, then run the full set before you submit.
`bazel run //:lint` runs the linters, `bazel run //:format` formats the code,
and `bazel build --config=linux-arm64 //...` type-checks another release
target. The CI workflow runs the same commands, and also builds every release
target and validates the scripts and the workflows.

## Make a focused change

- Keep each pull request focused on one issue or closely related change.
- Follow the existing Go style. CI checks gofumpt formatting and runs
  golangci-lint with the rules in `.golangci.yml`.
- Read [`AGENTS.md`](AGENTS.md) before changing the app. It describes where
  code goes, how background work reaches the UI, and how pages are added.
- Add or update tests for behavior users or other packages rely on. Prefer
  deterministic tests that do not need a live YouTube session, network access,
  or an audio device.
- Note user-visible changes under `Unreleased` in
  [`CHANGELOG.md`](CHANGELOG.md), which follows
  [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
- Avoid unrelated cleanup in a bug-fix pull request.
- Do not include cookies, session data, tokens, or other private account data
  in source files, test fixtures, screenshots, issue reports, or logs.
- Meiro is licensed under GPL-3.0. By submitting a contribution, you agree
  that it can be distributed under that license.

For UI changes, include a screenshot in the pull request when it helps reviewers
understand the result. For changes to playback, credentials, or platform
integration, describe the relevant environment and any platform you could not
test.

## Disclose AI and agent use

Disclose any generative AI or AI agent that helped with the contribution,
including design, implementation, tests, or documentation. Name the agent or
tool and summarize how you used it. You do not need to include private prompts
or entire chat logs. Review and understand all submitted changes; the
contributor remains responsible for the contribution.

Include a short section in your pull request description. If you did not use an
AI agent, say so:

```text
AI agents: None
```

If you used one or more AI agents, identify them and what they helped with:

```text
AI agents:
- <agent/tool/model>: <how it helped with this contribution>
```

## Open a pull request

In your pull request description:

1. Explain what changed and why.
2. Link the issue, if there is one. Use `Closes #123` when the pull request
   fully resolves that issue; otherwise use `Related to #123`.
3. List the checks you ran and their results. If you could not run a relevant
   check, say so.
4. Include the AI-agent disclosure above.
5. Add screenshots for UI changes when useful.

A maintainer will review the change and may ask for revisions. Thank you for
helping improve Meiro.

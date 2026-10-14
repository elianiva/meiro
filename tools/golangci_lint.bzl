"""Downloads the golangci-lint that `bazel run //:lint` runs.

The version is pinned here the same way .golangci.yml pins the linters, and it
is fetched as a release archive rather than compiled, so the lint reports what
CI reports and costs nothing to start. The checksums are the ones in the
release's golangci-lint-<version>-checksums.txt.
"""

load(":host_platform.bzl", "host_platform")

_VERSION = "2.14.0"

_SHA256 = {
    "darwin-amd64": "a5667c1c3536be1740133213e1e822bfb8f0d98ea12903174d6d5f635e4ed68d",
    "darwin-arm64": "5ef5f36a7147e91dc58ef9ef4d11bb7bad5ead0c76eb6c01327a73c641d1dcc3",
    "linux-amd64": "ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab",
    "linux-arm64": "ee7ec5f3453d15ddf106fae5a4d6c71737712348a979d1fe9cd52ec7ea299bae",
    "windows-amd64": "b15f903d1649283a2ab6e8d2f0c317eaf1f9f6b3763950e7e8b7e72a43dd724f",
}

def _golangci_lint_impl(rctx):
    platform = host_platform(rctx)
    if platform not in _SHA256:
        fail("golangci-lint: no release for " + platform)

    base = "golangci-lint-{v}-{p}".format(v = _VERSION, p = platform)
    extension = "zip" if platform.startswith("windows") else "tar.gz"
    url = "https://github.com/golangci/golangci-lint/releases/download/v{v}/{b}.{e}".format(
        v = _VERSION,
        b = base,
        e = extension,
    )
    binary = "golangci-lint.exe" if platform.startswith("windows") else "golangci-lint"
    rctx.download_and_extract(
        url = url,
        sha256 = _SHA256[platform],
        stripPrefix = base,
    )
    rctx.file("BUILD.bazel", """
# `bin` rather than the binary's own name: a filegroup named after the file it
# holds in the same package is a cycle.
filegroup(
    name = "bin",
    srcs = ["{binary}"],
    visibility = ["//visibility:public"],
)
""".format(binary = binary))

golangci_lint = repository_rule(
    implementation = _golangci_lint_impl,
    doc = "The pinned golangci-lint for the machine running Bazel.",
)

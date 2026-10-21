"""Downloads the golangci-lint that `bazel run //:lint` runs.

The version is pinned here the same way .golangci.yml pins the linters, and it
is fetched as a release archive rather than compiled, so the lint reports what
CI reports and costs nothing to start. The checksums are the ones in the
release's golangci-lint-<version>-checksums.txt.
"""

load(":host_platform.bzl", "host_platform")

_VERSION = "2.15.0"

_SHA256 = {
    "darwin-amd64": "767b899a28285e88ded2823d5dcc4bc5ef681173eb2d80f029a7b17ee4ece3b8",
    "darwin-arm64": "d1e409481a8ca846c4bc97e781eb5cb1bd5c23353a0b3977a1cbda1b1b6447d5",
    "linux-amd64": "9b7fe6a6bf6334b98decf8d86b11415ea7889afdabdd1959fdce23af2a9ebcea",
    "linux-arm64": "b4d8f17068b35ee6060cc2f69a16fb48740d706fbba81661c318649c114a6998",
    "windows-amd64": "cd7ea6063c95de245e17e5246e42bdc14bb221792c429dbd239ab3a6ade6ca6a",
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

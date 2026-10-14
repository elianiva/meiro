"""Downloads the gofumpt that `bazel run //:format` and `bazel run
//:format-check` run.

The version is pinned here the way .golangci.yml pins the linters, and it is
fetched as the release binary rather than compiled from go.mod. Reading a few
files takes no compiling, so the check costs nothing to start; compiling it
from source put the whole Go standard library in front of it, which is what a
format check must never wait for.

The release publishes no checksums file, so the checksums below are the
SHA-256 of the release's own binaries, as in
`sha256sum gofumpt_v0.12.0_linux_amd64`; the version and they change together.
"""

load(":host_platform.bzl", "host_platform")

_VERSION = "0.12.0"

_SHA256 = {
    "darwin-amd64": "1d3626892a321aca96a891696eaecb8f68e69e90582c2b55672d5ca210fd5fa8",
    "darwin-arm64": "1a3e325b956ed12c21031d9e13382bd51a4fabb57de8d40428819b9583c3cfb9",
    "linux-amd64": "3bc4fcad497439918f5f56d4987de4709a99da0a355e6bec13a7278532b01d38",
    "linux-arm64": "4322f081241ab0618356aa5921a8c110e4c7acfc17175cc15dc57b2f4a4b652d",
    "windows-amd64": "66bd679908b64864fc85417e7ea01148774b477364b9a02ab1c194333a55c997",
}

def _gofumpt_impl(rctx):
    platform = host_platform(rctx)
    if platform not in _SHA256:
        fail("gofumpt: no release for " + platform)

    # The asset is the binary itself, named gofumpt_v<version>_<os>_<arch>,
    # so it is downloaded under that name and marked executable.
    name = "gofumpt_v{v}_{p}".format(
        v = _VERSION,
        p = platform.replace("-", "_"),
    )
    if platform.startswith("windows"):
        name += ".exe"
    rctx.download(
        url = "https://github.com/mvdan/gofumpt/releases/download/v{v}/{n}".format(
            v = _VERSION,
            n = name,
        ),
        output = name,
        sha256 = _SHA256[platform],
        executable = True,
    )
    rctx.file("BUILD.bazel", """
filegroup(
    name = "gofumpt",
    srcs = ["{name}"],
    visibility = ["//visibility:public"],
)
""".format(name = name))

gofumpt = repository_rule(
    implementation = _gofumpt_impl,
    doc = "The pinned gofumpt for the machine running Bazel.",
)

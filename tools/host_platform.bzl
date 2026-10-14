"""The machine running Bazel, named the way release archives name platforms.

The tools the build pins are downloaded as the release for the host, so each
of their repository rules has to spell the host out: this is the one place
that does, and it names it goos-goarch (linux-amd64), which is how those
releases name their artifacts.
"""

def host_platform(rctx):
    """The goos-goarch name of the machine running Bazel."""
    name = rctx.os.name.lower()
    if name.startswith("linux"):
        goos = "linux"
    elif name.startswith("mac") or name.startswith("darwin"):
        goos = "darwin"
    elif name.startswith("windows"):
        goos = "windows"
    else:
        fail("no release for " + rctx.os.name)

    arch = rctx.os.arch.lower()
    if arch in ("aarch64", "arm64"):
        goarch = "arm64"
    elif arch in ("x86_64", "amd64"):
        goarch = "amd64"
    else:
        fail("no release for " + rctx.os.arch)

    return goos + "-" + goarch

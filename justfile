# The platform of this machine, as scripts/fetch-tools.sh and mygo name it.
host := `case "$(uname -s)-$(uname -m)" in Darwin-arm64) echo darwin-arm64;; Darwin-x86_64) echo darwin-amd64;; Linux-x86_64) echo linux-amd64;; Linux-aarch64|Linux-arm64) echo linux-arm64;; *) echo unsupported;; esac`

# The tools CI pins. Keep these in step with .github/workflows/ci.yml, which
# reads the same two versions.
golangci_lint := "v2.14.0"
actionlint := "1.7.9"

[private]
default:
    @just --list

# Run the app with live reload, using ffmpeg and yt-dlp from PATH.
dev:
    go tool mygo dev

# Vet and test everything.
test:
    go vet ./...
    go test -race ./...

# Run the checks CI runs, the same way, so drift is caught before a push.
ci:
    #!/usr/bin/env bash
    set -euo pipefail

    echo "== formatting =="
    # gofumpt is gofmt with more rules, so it is the one check to make.
    test -z "$(go tool gofumpt -l .)" || { echo "Files needing gofumpt:"; go tool gofumpt -l .; exit 1; }

    echo "== go.mod and go.sum =="
    go mod tidy -diff

    echo "== vet and build every target =="
    # Every target the release ships, and Windows, because a break in a
    # platform file compiles nowhere else.
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
        goos="${target%%/*}"; goarch="${target##*/}"
        echo "-- $target"
        GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go vet ./...
        GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build ./...
    done

    # The pinned version, run without installing it, the way CI's action does.
    echo "== lint =="
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{golangci_lint}} run ./...

    # The Linux media session needs a session bus, as it does in CI.
    echo "== test =="
    if command -v dbus-run-session >/dev/null 2>&1; then
        dbus-run-session -- go test -race -count=1 ./...
    else
        echo "warning: dbus-run-session is missing; D-Bus tests will skip themselves."
        go test -race -count=1 ./...
    fi

    echo "== scripts and workflows =="
    if command -v shellcheck >/dev/null 2>&1; then
        shellcheck --severity=warning scripts/fetch-tools.sh scripts/build-ffmpeg.sh scripts/package-linux.sh
    else
        echo "warning: shellcheck is missing; the scripts were not checked."
    fi
    if command -v actionlint >/dev/null 2>&1; then
        actionlint -color
    else
        echo "warning: actionlint is missing; the workflows were not checked. CI pins {{actionlint}}."
    fi

    echo "All checks passed."

# Get the ffmpeg and yt-dlp a build ships, for one platform.
tools platform=host:
    scripts/fetch-tools.sh {{platform}}

# Build a production app with its tools inside, into build/.
build platform=host: (tools platform)
    go tool mygo build -platform {{replace(platform, "-", "/")}}

# Build this machine's production app and open it.
run: (build host)
    #!/usr/bin/env bash
    set -euo pipefail
    case "{{host}}" in
        darwin-*) open "build/{{host}}/Meiro.app" ;;
        *) "build/{{host}}/meiro" ;;
    esac

# Remove build output and the downloaded tools.
clean:
    rm -rf build .mygo resources/darwin-* resources/linux-* resources/windows-*

# The platform of this machine, as scripts/fetch-tools.sh and mygo name it.
host := `case "$(uname -s)-$(uname -m)" in Darwin-arm64) echo darwin-arm64;; Darwin-x86_64) echo darwin-amd64;; Linux-x86_64) echo linux-amd64;; Linux-aarch64|Linux-arm64) echo linux-arm64;; *) echo unsupported;; esac`

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

# Download the ffmpeg and yt-dlp a build ships, for one platform.
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

# Render the app's pages to PNG files, for looking at them.
snapshots dir="/tmp/meiro-shots":
    mkdir -p {{dir}}
    MEIRO_SNAPSHOTS={{dir}} go test -tags snapshot -run TestSnapshots .

# Remove build output and the downloaded tools.
clean:
    rm -rf build .mygo resources/darwin-* resources/linux-* resources/windows-*

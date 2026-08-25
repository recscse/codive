#!/usr/bin/env bash
# scripts/build.sh - Cross-platform build script for POSIX/Bash
set -euo pipefail

VERSION="${1:-v1.1.0}"
COMMIT="${2:-$(git rev-parse --short HEAD 2>/dev/null || echo "dev")}"
DATE="${3:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"

LDFLAGS="-s -w -X main.Version=${VERSION} -X main.GitCommit=${COMMIT} -X main.BuildDate=${DATE}"

TARGETS=(
    "windows/amd64/.exe"
    "windows/arm64/.exe"
    "darwin/amd64/"
    "darwin/arm64/"
    "linux/amd64/"
    "linux/arm64/"
)

mkdir -p dist
rm -rf dist/*

echo "Building ctxd ${VERSION} binaries across 6 targets..."

for item in "${TARGETS[@]}"; do
    IFS="/" read -r os arch ext <<< "$item"
    output="dist/ctxd-${os}-${arch}${ext}"
    echo "  -> Building ${output} (${os}/${arch})..."
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build -ldflags "${LDFLAGS}" -o "${output}" .
done

echo ""
echo "All release binaries successfully built in dist/:"
ls -lh dist/

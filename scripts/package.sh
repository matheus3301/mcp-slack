#!/usr/bin/env bash
#
# package.sh builds the release binaries for every supported platform,
# packages a platform-appropriate archive for each, and writes a SHA256SUMS
# file over the archives. It is intentionally small and dependency-free so it
# reads the same whether run locally or in CI.
#
# Usage:
#   VERSION=v0.1.0 COMMIT=$(git rev-parse HEAD) DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     scripts/package.sh [outdir]
#
# Each archive contains the binary at its root plus LICENSE and README.md.
# Archives: tar.gz for linux/darwin, zip for windows.
set -euo pipefail

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-}"
DATE="${DATE:-}"
OUTDIR="${1:-dist}"
PKG="./cmd/mcp-slack"

# os/arch pairs to build.
PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"

LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"

for platform in "${PLATFORMS[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"

  bin="mcp-slack"
  [ "$os" = "windows" ] && bin="mcp-slack.exe"

  stage="$(mktemp -d)"
  echo "building ${os}/${arch}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    "${GO:-go}" build -trimpath -ldflags "$LDFLAGS" -o "$stage/$bin" "$PKG"
  cp LICENSE README.md "$stage/"

  base="mcp-slack_${VERSION}_${os}_${arch}"
  if [ "$os" = "windows" ]; then
    ( cd "$stage" && zip -q -X "$base.zip" "$bin" LICENSE README.md )
    mv "$stage/$base.zip" "$OUTDIR/"
  else
    # -C into the stage dir so paths inside the tarball are flat.
    tar -czf "$OUTDIR/$base.tar.gz" -C "$stage" "$bin" LICENSE README.md
  fi
  rm -rf "$stage"
done

# Checksums over the archives, with stable (basename) paths.
(
  cd "$OUTDIR"
  files=(*.tar.gz *.zip)
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${files[@]}" > SHA256SUMS
  else
    shasum -a 256 "${files[@]}" > SHA256SUMS
  fi
)

echo "artifacts in $OUTDIR:"
ls -1 "$OUTDIR"

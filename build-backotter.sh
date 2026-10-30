#!/usr/bin/env bash
# Build BackOtter's restic for each GOOS/GOARCH given (default: linux/amd64
# and freebsd/amd64) into dist/.  See BACKOTTER.md.

set -euo pipefail

cd "$(dirname "$0")"
targets=("$@")
[[ ${#targets[@]} -gt 0 ]] || targets=(linux/amd64 freebsd/amd64)
version=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/global/global.go)
[[ $version == *+backotter.* ]] || { echo "build-backotter: Version is '$version', not X.Y.Z+backotter.N" >&2; exit 1; }

mkdir -p dist
for t in "${targets[@]}"; do
    goos=${t%/*} goarch=${t#*/}
    out=dist/restic-backotter-$version-$goos-$goarch
    # No "selfupdate" tag: self-update would replace this build with stock
    # restic.  CGO_ENABLED=0 and -trimpath as restic's own release builds.
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
        go build -trimpath -ldflags "-s -w" -o "$out" ./cmd/restic
    echo "$out"
done

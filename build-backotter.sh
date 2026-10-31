#!/usr/bin/env bash
# Build BackOtter's restic for each GOOS/GOARCH given (default: linux/amd64,
# freebsd/amd64, netbsd/amd64 and dragonfly/amd64) into dist/, and say in
# dist/restic-commit what they were built from.  See BACKOTTER.md.
#
# --source-digest prints only the digest of the Go sources that
# dist/restic-commit records, without building (no Go needed), to check a
# build made from a copy of the tree against a checkout.
#
# RESTIC_COMMIT names the commit of a tree without .git (a copy made with
# rsync), which restic-commit otherwise calls unknown.

set -euo pipefail

cd "$(dirname "$0")"

# The digest of every .go file, go.mod and go.sum, by path and content: the
# same for a checkout and an rsync copy of it, whatever their mtimes.
source_digest() {
    local sum=sha256sum
    command -v sha256sum >/dev/null || sum="shasum -a 256"
    find . \( -path ./.git -o -path ./dist \) -prune -o -type f \
        \( -name '*.go' -o -name go.mod -o -name go.sum \) -print |
        LC_ALL=C sort | tr '\n' '\0' | xargs -0 $sum | $sum | cut -d' ' -f1
}
if [[ ${1:-} == --source-digest ]]; then
    source_digest
    exit 0
fi

targets=("$@")
[[ ${#targets[@]} -gt 0 ]] || targets=(linux/amd64 freebsd/amd64 netbsd/amd64 dragonfly/amd64)
version=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/global/global.go)
[[ $version == *+backotter.* ]] || { echo "build-backotter: Version is '$version', not X.Y.Z+backotter.N" >&2; exit 1; }

# The commit, and whether the tree has changes.  Only the tree's own .git
# counts: git would take a copy inside some other checkout for part of it.
commit=${RESTIC_COMMIT:-}
if [[ -e .git ]]; then
    [[ -n $commit ]] || commit=$(git rev-parse HEAD 2>/dev/null) || commit=""
    [[ -z $(git status --porcelain 2>/dev/null) ]] || commit="${commit:-unknown}, with uncommitted changes"
fi
commit="${commit:-unknown (no .git, no RESTIC_COMMIT)} (from $PWD)"

mkdir -p dist
rm -f dist/restic-commit
for t in "${targets[@]}"; do
    goos=${t%/*} goarch=${t#*/}
    out=dist/restic-backotter-$version-$goos-$goarch
    # No "selfupdate" tag: self-update would replace this build with stock
    # restic.  CGO_ENABLED=0 and -trimpath as restic's own release builds.
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
        go build -trimpath -ldflags "-s -w" -o "$out" ./cmd/restic
    echo "$out"
done
printf '%s\nsource digest %s\n%s, built %s on %s\n' "$commit" "$(source_digest)" \
    "$(go version)" "$(date -u '+%Y-%m-%d %H:%M UTC')" "$(uname -n)" >dist/restic-commit

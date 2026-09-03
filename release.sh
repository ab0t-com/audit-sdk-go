#!/bin/bash
#
# release.sh — cut a release of audit-sdk-go (PUBLIC repo).
#
# Dry-run by default: runs EVERY public-safety + green gate and reports. With --push it also
# bumps version.go, commits, tags vX.Y.Z, and pushes the tag, then verifies it is fetchable via
# the Go proxy. See RELEASE.md for the SOP.
#
# Usage:
#   ./release.sh 0.1.1            # dry-run (gates only, no writes)
#   ./release.sh 0.1.1 --push     # gates + bump + commit + tag + push
#
set -euo pipefail

VERSION="${1:-}"
MODE="${2:-dryrun}"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_DIR"

[ -n "$VERSION" ] || { echo "usage: ./release.sh X.Y.Z [--push]"; exit 1; }
echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "❌ version must be SemVer X.Y.Z (no 'v'): got '$VERSION'"; exit 1; }
TAG="v$VERSION"

fail() { echo "❌ $1"; exit 1; }
ok()   { echo "✓ $1"; }

echo "── Release gates for $TAG ──────────────────────────────────────────────"

# 1. Green: fmt / build / vet / test
[ -z "$(gofmt -l . 2>/dev/null)" ] || { gofmt -l .; fail "gofmt: files need formatting"; }; ok "gofmt clean"
go build ./... >/dev/null || fail "go build failed"; ok "go build"
go vet ./... >/dev/null 2>&1 || fail "go vet failed"; ok "go vet"
go test -race -count=1 ./... >/dev/null 2>&1 || fail "go test -race failed"; ok "go test -race"

# 2. Stdlib-only preserved (no go.sum, no require block)
[ ! -f go.sum ] || fail "go.sum exists — a dependency crept in (must stay stdlib-only)"
grep -qE '^\s*require ' go.mod && fail "go.mod has a require block — must stay stdlib-only" || ok "stdlib-only (no go.sum / require)"

# 3. No secrets
if command -v gitleaks >/dev/null; then
    gitleaks detect --no-git --source . --config .gitleaks.toml >/dev/null 2>&1 || fail "gitleaks found a leak"
    ok "gitleaks: no leaks"
else
    echo "⚠ gitleaks not installed — CI will still enforce it, but install it locally to gate here"
fi

# 4. No internal references (public-safety)
LEAK_REFS=$(grep -rniE 'connectgo|intergration|audit-ingressd|/home/ubuntu|infra/infra|internal/platform|PP-AUDIT|SDK_CONFORMANCE|_20260[0-9]{3}' . \
    --include='*.go' --include='*.md' --include='*.toml' --include='*.yml' 2>/dev/null | grep -v '\.git/' || true)
[ -z "$LEAK_REFS" ] || { echo "$LEAK_REFS"; fail "internal references found — scrub before releasing publicly"; }
ok "no internal references"

# 5. version.go will match the tag
CURRENT=$(grep -oE 'Version = "[^"]+"' version.go | sed -E 's/.*"([^"]+)".*/\1/')
echo "  version.go currently: $CURRENT → releasing: $VERSION"

if [ "$MODE" != "--push" ]; then
    echo "── DRY-RUN complete. All gates green. Re-run with --push to release $TAG. ──"
    exit 0
fi

echo "── PUSH mode: bump + commit + tag + push $TAG ──────────────────────────"
[ -z "$(git status --porcelain | grep -v '^??')" ] || fail "working tree has uncommitted tracked changes — commit or stash first"

# Bump version.go
sed -i -E "s/Version = \"[^\"]+\"/Version = \"$VERSION\"/" version.go
git add version.go RELEASE.md
git commit -q -m "release $TAG

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>" || echo "  (nothing to commit for version bump)"

git tag -a "$TAG" -m "audit-sdk-go $TAG"
git push origin main --follow-tags
ok "pushed $TAG"

echo "Verifying $TAG is fetchable via the Go proxy (may lag ~1 min)…"
GOFLAGS='' GOPROXY=https://proxy.golang.org,direct go list -m "github.com/ab0t-com/audit-sdk-go@$TAG" 2>&1 | head -1 \
    && ok "released + fetchable: github.com/ab0t-com/audit-sdk-go@$TAG" \
    || echo "  (proxy may lag; the tag is pushed — retry the go list in a minute)"

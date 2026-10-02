#!/bin/sh
#
# Print the version string baked into BirdNET-Go builds. One line on stdout,
# always exit 0: Task evaluates the global VERSION variable on every
# invocation and aborts every task when that command fails.
#
# Rules, first match wins:
#   1. BUILD_VERSION is set and non-empty: printed verbatim.
#   2. A release tag points at HEAD: that tag (for example 20260823).
#      Tags in NON_RELEASE_TAGS are never release tags. Tags not named
#      nightly-* win over nightly-* tags on the same commit. Within a group
#      the newest-created tag wins, ties go to the highest version-aware name.
#   3. Any other build inside a git work tree root: <HEAD committer date in
#      UTC as YYYYMMDD>-g<HASH_LENGTH-char hash>-dev (for example
#      20260927-g5dc2ab881-dev). The commit date, not the build date, keeps
#      the string reproducible. Uncommitted changes add no suffix.
#   4. No git, no git metadata, or a source tree nested inside another
#      repository (a tarball unpacked below some unrelated checkout): unknown.
#
# Consumers that rely on this format: versionDatePattern in
# internal/diagnostics/anomaly.go reads the leading YYYYMMDD, ClassifyTag in
# internal/update/manifest/manifest.go must keep the dev form out of every
# release channel, and the live check in .github/workflows/build-version.yml
# rebuilds the dev form and the manifest exclusion on its own. Update all three
# when the format changes.

set -u

# Tags that exist in the repository but are not releases. "manifest" is the
# permanent prerelease that carries the auto-update manifest.json asset
# (.github/workflows/release-manifest.yml).
NON_RELEASE_TAGS="manifest"
# Tag prefix of nightly builds, which lose to any other release tag.
NIGHTLY_PREFIX="nightly-"
HASH_LENGTH=9
DEV_SUFFIX=dev
UNKNOWN_VERSION=unknown

# unknown prints the fallback version and exits successfully.
unknown() {
    printf '%s\n' "$UNKNOWN_VERSION"
    exit 0
}

# is_non_release succeeds when the tag is listed in NON_RELEASE_TAGS.
is_non_release() {
    for excluded in $NON_RELEASE_TAGS; do
        [ "$1" = "$excluded" ] && return 0
    done
    return 1
}

if [ -n "${BUILD_VERSION:-}" ]; then
    printf '%s\n' "$BUILD_VERSION"
    exit 0
fi

case $0 in
*/*) script_dir=${0%/*} ;;
*) script_dir=. ;;
esac
CDPATH='' cd -- "$script_dir/.." 2>/dev/null || unknown

command -v git >/dev/null 2>&1 || unknown
# Prints "true" plus an empty prefix line only at the root of a work tree; a
# non-empty prefix means the source root is a subdirectory of some other repo.
[ "$(git rev-parse --is-inside-work-tree --show-prefix 2>/dev/null | tr -d '\r')" = true ] || unknown

# Tag names never contain glob characters that matter, but disable globbing so
# the unquoted expansion below cannot expand anything.
set -f
tags=$(git tag --points-at HEAD --sort=-version:refname --sort=-creatordate 2>/dev/null | tr -d '\r')

# The first non-nightly release tag wins; a nightly tag only when there is none.
nightly=
for tag in $tags; do
    is_non_release "$tag" && continue
    case $tag in
    "$NIGHTLY_PREFIX"*)
        [ -n "$nightly" ] || nightly=$tag
        continue
        ;;
    esac
    printf '%s\n' "$tag"
    exit 0
done
if [ -n "$nightly" ]; then
    printf '%s\n' "$nightly"
    exit 0
fi

# One git log call gives both parts. log.showSignature=true in a user's config
# makes git log print gpg lines on stdout, so it is switched off here.
dev_base=$(TZ=UTC0 git -c log.showSignature=false log -1 --abbrev="$HASH_LENGTH" \
    --date=format-local:%Y%m%d --format=%cd-g%h HEAD 2>/dev/null | tr -d '\r')
[ -n "$dev_base" ] || unknown
printf '%s-%s\n' "$dev_base" "$DEV_SUFFIX"

#!/usr/bin/env bash
#
# Tests for scripts/build-version.sh.
#
# Each case builds a throwaway git repository with fixed dates, copies the
# script into <repo>/scripts and runs it from /, so the result cannot depend on
# the caller's working directory. The environment setup below keeps the
# caller's git configuration and repository variables out of it.
#
# Run: scripts/build-version_test.sh   (exit 0 = all pass). Needs git.

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${REPO_ROOT}/scripts/build-version.sh"
[ -f "$SCRIPT" ] || { echo "FATAL: $SCRIPT not found" >&2; exit 2; }
command -v git >/dev/null 2>&1 || { echo "FATAL: git not found" >&2; exit 2; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Drop repository-locating variables (GIT_DIR, GIT_INDEX_FILE, GIT_WORK_TREE
# and the rest) that a git hook or wrapper may export; otherwise the fixture
# commands below would act on the caller's repository.
# shellcheck disable=SC2046
unset $(git rev-parse --local-env-vars)
# Isolate git from the user's and the system configuration.
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
# Fixture repos live under $WORK; never let git discover a repo above it.
export GIT_CEILING_DIRECTORIES="$WORK"
unset BUILD_VERSION

PASS=0
FAIL=0
CURRENT_TEST=""
VERSION_PATTERN='^[0-9A-Za-z._+-]+$'

it() { CURRENT_TEST="$1"; }

assert_eq() { # description expected actual
    if [ "$2" = "$3" ]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        printf 'FAIL [%s] %s\n       expected: [%s]\n       actual:   [%s]\n' "$CURRENT_TEST" "$1" "$2" "$3" >&2
    fi
}

# new_repo <name>: create an empty repo with the script installed.
new_repo() {
    local dir="$WORK/$1"
    mkdir -p "$dir/scripts"
    git -C "$dir" init -q -b main
    git -C "$dir" config user.name test
    git -C "$dir" config user.email test@example.invalid
    cp "$SCRIPT" "$dir/scripts/build-version.sh"
    echo "$dir"
}

# commit <dir> <iso-date> [message]
commit() {
    local dir="$1" date="$2" msg="${3:-c}"
    echo "$msg $date" >>"$dir/file.txt"
    git -C "$dir" add -A
    GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" git -C "$dir" commit -q -m "$msg"
}

# tag_at <dir> <tag> <iso-date> [annotated]: tag HEAD. The date sets the
# tagger date of an annotated tag; a lightweight tag always carries the commit
# date, so the argument only documents the intent there.
tag_at() {
    local dir="$1" tag="$2" date="$3"
    if [ "${4:-}" = annotated ]; then
        GIT_COMMITTER_DATE="$date" git -C "$dir" tag -a -m "$tag" "$tag"
    else
        git -C "$dir" tag "$tag"
    fi
}

# run_version <dir> [env assignments...]: run the installed script from /.
run_version() {
    local dir="$1"
    shift
    (cd / && env "$@" sh "$dir/scripts/build-version.sh")
}

short() { git -C "$1" rev-parse --short=9 HEAD; }

check_shape() { # description output status
    assert_eq "$1 exits 0" 0 "$3"
    if [[ "$2" =~ $VERSION_PATTERN ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        printf 'FAIL [%s] %s output has invalid characters: [%s]\n' "$CURRENT_TEST" "$1" "$2" >&2
    fi
}

D1="2026-08-23T12:00:00+00:00"
D2="2026-10-02T09:00:00+00:00"

it "BUILD_VERSION pin wins"
r=$(new_repo pin); commit "$r" "$D1"
assert_eq "pin printed verbatim" "custom-1.2" "$(run_version "$r" BUILD_VERSION=custom-1.2)"

it "empty BUILD_VERSION counts as unset"
assert_eq "empty pin falls through to dev form" "20260823-g$(short "$r")-dev" "$(run_version "$r" BUILD_VERSION=)"

it "HEAD on a release tag gives the tag"
r=$(new_repo reltag); commit "$r" "$D1"; tag_at "$r" 20260823 "$D1"
out=$(run_version "$r"); st=$?
assert_eq "tag printed" 20260823 "$out"; check_shape "release tag" "$out" "$st"

it "commits after a tag give the dev form"
commit "$r" "$D2"
out=$(run_version "$r"); st=$?
assert_eq "dev form" "20261002-g$(short "$r")-dev" "$out"; check_shape "dev form" "$out" "$st"

it "manifest alone on HEAD gives the dev form"
r=$(new_repo manifest); commit "$r" "$D1"; tag_at "$r" manifest "$D1"
assert_eq "manifest excluded" "20260823-g$(short "$r")-dev" "$(run_version "$r")"

it "manifest plus a release tag gives the release"
tag_at "$r" 20260823 "$D1"
assert_eq "release tag preferred over manifest" 20260823 "$(run_version "$r")"

it "newest-created tag wins among several"
r=$(new_repo several); commit "$r" "$D1"
# The highest version name (20260901) is not the newest-created tag, so only the
# creation-date sort picks 20260823.
tag_at "$r" 20260901 "2026-08-01T00:00:00+00:00" annotated
tag_at "$r" 20260823 "2026-08-23T00:00:00+00:00" annotated
tag_at "$r" 20260702 "2026-07-02T00:00:00+00:00" annotated
assert_eq "newest creator date" 20260823 "$(run_version "$r")"

it "lightweight tie picks the highest version name"
r=$(new_repo tie); commit "$r" "$D1"
tag_at "$r" nightly-20260429-404 "$D1"; tag_at "$r" nightly-20260429-405 "$D1"
assert_eq "highest version-aware name" nightly-20260429-405 "$(run_version "$r")"

it "lightweight tie compares numbers by value, not by text"
# Plain name order would put -99 above -100. Real nightly tags carry no
# counter today; this pins the documented version-aware tie rule.
r2=$(new_repo tiewidth); commit "$r2" "$D1"
tag_at "$r2" nightly-20260429-99 "$D1"; tag_at "$r2" nightly-20260429-100 "$D1"
assert_eq "version order across digit counts" nightly-20260429-100 "$(run_version "$r2")"

it "release tag beats nightly tag on the same commit"
tag_at "$r" 20260429 "$D1"
assert_eq "date tag over nightly" 20260429 "$(run_version "$r")"
r=$(new_repo promoted); commit "$r" "$D1"
tag_at "$r" 20260823 "2026-08-23T00:00:00+00:00" annotated
tag_at "$r" nightly-20260823 "2026-08-24T00:00:00+00:00" annotated
assert_eq "date tag over a newer nightly" 20260823 "$(run_version "$r")"

it "any tag naming counts as a release"
r=$(new_repo naming); commit "$r" "$D1"; tag_at "$r" rel-2027.01 "$D1"
assert_eq "arbitrary tag" rel-2027.01 "$(run_version "$r")"

# POSIX TZ strings need no tzdata, so these cases discriminate on any runner,
# including a UTC one. XXX-3 is three hours east of UTC.
it "committer date is converted to UTC"
r=$(new_repo utc); commit "$r" "2026-09-28T01:30:00+03:00"
assert_eq "previous UTC day" "20260927-g$(short "$r")-dev" "$(run_version "$r" TZ=XXX-3)"

it "result does not depend on TZ"
# 11:00 UTC on the 27th is already the 28th at UTC+14 and still the 26th at UTC-12.
r=$(new_repo tz); commit "$r" "2026-09-27T11:00:00+00:00"
assert_eq "TZ=XXX-14" "20260927-g$(short "$r")-dev" "$(run_version "$r" TZ=XXX-14)"
assert_eq "TZ=XXX+12" "20260927-g$(short "$r")-dev" "$(run_version "$r" TZ=XXX+12)"

it "committer date is used, not author date"
r=$(new_repo authordate); echo a >"$r/file.txt"; git -C "$r" add -A
GIT_AUTHOR_DATE="2026-07-01T12:00:00+00:00" GIT_COMMITTER_DATE="$D1" git -C "$r" commit -q -m rebased
assert_eq "committer date" "20260823-g$(short "$r")-dev" "$(run_version "$r")"

it "repository without commits gives unknown"
r=$(new_repo empty)
out=$(run_version "$r"); st=$?
assert_eq "unborn HEAD" unknown "$out"; check_shape "unborn HEAD" "$out" "$st"

it "shallow clone without tags gives the dev form"
r=$(new_repo shallowsrc); commit "$r" "$D1"; tag_at "$r" 20260823 "$D1"; commit "$r" "$D2"
git clone -q --depth 1 "file://$r" "$WORK/shallow" 2>/dev/null
assert_eq "dev form in shallow clone" "20261002-g$(short "$WORK/shallow")-dev" "$(run_version "$WORK/shallow")"

it "shallow clone of a tag gives the tag"
tag_at "$r" 20261002 "$D2"
git clone -q --depth 1 --branch 20261002 "file://$r" "$WORK/shallowtag" 2>/dev/null
assert_eq "tag in shallow clone" 20261002 "$(run_version "$WORK/shallowtag")"

it "outside a repository gives unknown"
mkdir -p "$WORK/plain/scripts"; cp "$SCRIPT" "$WORK/plain/scripts/"
out=$(run_version "$WORK/plain"); st=$?
assert_eq "no repo" unknown "$out"; check_shape "no repo" "$out" "$st"

it "tree nested in another repository gives unknown"
r=$(new_repo outer); commit "$r" "$D1"
mkdir -p "$r/vendor/pkg/scripts"; cp "$SCRIPT" "$r/vendor/pkg/scripts/"
out=$(run_version "$r/vendor/pkg"); st=$?
assert_eq "nested" unknown "$out"; check_shape "nested" "$out" "$st"

it "missing git gives unknown"
r=$(new_repo nogit); commit "$r" "$D1"
out=$(cd / && PATH=/nonexistent /bin/sh "$r/scripts/build-version.sh"); st=$?
assert_eq "no git binary" unknown "$out"; check_shape "no git" "$out" "$st"

it "uncommitted changes add no suffix"
r=$(new_repo dirty); commit "$r" "$D1"
clean=$(run_version "$r")
assert_eq "clean tree dev form" "20260823-g$(short "$r")-dev" "$clean"
echo change >>"$r/file.txt"; echo new >"$r/untracked.txt"
assert_eq "dirty tree same as clean" "$clean" "$(run_version "$r")"

it "log.showSignature does not leak into the version"
# A stub gpg signs the commit and, on verify, prints the kind of line real gpg
# writes, which git log copies to stdout when log.showSignature is set.
r=$(new_repo signed)
cat >"$WORK/fakegpg" <<'EOF'
#!/bin/sh
for arg in "$@"; do
    if [ "$arg" = --verify ]; then
        echo "gpg: Signature made by fake key" >&2
        echo "[GNUPG:] GOODSIG 0 fake"
        exit 0
    fi
done
cat >/dev/null
echo "[GNUPG:] SIG_CREATED D 1 8 00 0 0" >&2
printf -- '-----BEGIN PGP SIGNATURE-----\n\nZmFrZQ==\n-----END PGP SIGNATURE-----\n'
EOF
chmod +x "$WORK/fakegpg"
git -C "$r" config gpg.program "$WORK/fakegpg"
echo signed >"$r/file.txt"; git -C "$r" add -A
GIT_AUTHOR_DATE="$D1" GIT_COMMITTER_DATE="$D1" git -C "$r" commit -q -S -m signed
git -C "$r" config log.showSignature true
out=$(run_version "$r"); st=$?
assert_eq "signature lines dropped" "20260823-g$(short "$r")-dev" "$out"; check_shape "signed commit" "$out" "$st"

echo
echo "build-version tests: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]

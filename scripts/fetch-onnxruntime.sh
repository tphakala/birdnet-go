#!/bin/sh
#
# Fetch the ONNX Runtime shared libraries for one platform, verify them against
# the SHA-256 pins in onnxruntime-pins.sha256 (next to this script) and lay them
# out in a library directory. No file reaches the directory before its hash
# matched a pin.
#
# Usage:
#   fetch-onnxruntime.sh install VERSION PLATFORM DEST_DIR
#   fetch-onnxruntime.sh pins VERSION DOWNLOAD_DIR
#
# PLATFORM is linux-amd64, linux-arm64, darwin-arm64 or windows-amd64
# (darwin-amd64 only with the github source). It names the target, not the
# host, so any host can fetch any platform.
#
# install owns DEST_DIR: it must be missing, empty, or hold this version's files
# and nothing else. When it already holds the pinned files, install only
# verifies them and repairs the links, without a download. It always ends by
# checking that DEST_DIR holds exactly the pinned files and their links, so a
# directory restored from a cache is safe to copy from with a glob.
#
# The script deletes nothing. Downloads and staged files live in the download
# directory DEST_DIR.download, a sibling of DEST_DIR, and stay there: a later
# run reuses a package whose hash matches its pin and overwrites the rest.
# Verified files are renamed from there into DEST_DIR.
#
# pins prints a pin file for VERSION on stdout, using DOWNLOAD_DIR the same
# way. It trusts what it downloads, so compare the package hash with the
# publisher's before committing. With the github source it prints the library
# pins of the release archives and carries the package pin over from the
# current pin file.
#
# Environment:
#   ONNXRUNTIME_SOURCE   nuget or github; overrides the default below.
#   ONNXRUNTIME_ARCHIVE  use this local archive instead of downloading one.
#   ONNXRUNTIME_PINS     pin file to read instead of the one next to the script.

set -eu

# Download source, the one switch for every consumer (Taskfile, CI, Dockerfile):
#   nuget   Microsoft.ML.OnnxRuntime package from api.nuget.org
#   github  release archives from github.com/microsoft/onnxruntime
ONNXRUNTIME_SOURCE="${ONNXRUNTIME_SOURCE:-nuget}"

NUGET_BASE_URL="https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime"
GITHUB_BASE_URL="https://github.com/microsoft/onnxruntime/releases/download"
ALL_PLATFORMS="linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64"
LIB_MODE=755

PINS="${ONNXRUNTIME_PINS:-$(dirname "$0")/onnxruntime-pins.sha256}"
DL=""
ARCHIVE=""

die() {
    echo "fetch-onnxruntime: $*" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

# sha256_of FILE: print the SHA-256 of FILE. The file is read from stdin: with a
# file name argument the tools escape the digest of a name that holds a
# backslash.
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sum=$(sha256sum <"$1") || return 1
    else
        sum=$(shasum -a 256 <"$1") || return 1
    fi
    echo "${sum%% *}"
}

# pin_of LABEL: print the pinned hash of LABEL, or nothing when it has none.
# Fails on a label pinned twice.
pin_of() {
    awk -v label="$1" '
        { sub(/\r$/, "") }
        /^#/ || /^[[:space:]]*$/ { next }
        $0 !~ /^[0-9a-f]+  [^[:space:]]+$/ || length($1) != 64 { bad = 1; next }
        $2 == label { n++; hash = $1 }
        END { if (bad || n > 1) exit 1; if (n == 1) print hash }
    ' <"$PINS" || die "malformed or duplicate lines in $PINS"
}

# matches_pin FILE LABEL: succeed when FILE is readable and has LABEL's pin.
matches_pin() {
    want=$(pin_of "$2")
    [ -n "$want" ] || return 1
    got=$(sha256_of "$1") || return 1
    [ -n "$got" ] && [ "$got" = "$want" ]
}

# verify FILE LABEL: fail unless FILE has the hash pinned for LABEL.
verify() {
    want=$(pin_of "$2")
    [ -n "$want" ] || die "no pin for $2 in $PINS; add a '<sha256>  $2' line after reviewing the bytes (the pins command is described in the header of $0)"
    got=$(sha256_of "$1") || die "cannot hash $1"
    [ "$got" = "$want" ] || die "SHA-256 mismatch for $2 from the $ONNXRUNTIME_SOURCE source:
  pinned $want
  got    $got
Nothing was installed. If the $ONNXRUNTIME_SOURCE source is meant to ship different bytes, review them and refresh the pins with: $0 pins $VERSION DOWNLOAD_DIR"
}

# set_platform PLATFORM: set RID (NuGet runtime id, empty when the package has
# none), ASSET (GitHub release archive) and FILES (library files to install).
set_platform() {
    case "$1" in
        linux-amd64) RID=linux-x64 ASSET="onnxruntime-linux-x64-$VERSION.tgz" ;;
        linux-arm64) RID=linux-arm64 ASSET="onnxruntime-linux-aarch64-$VERSION.tgz" ;;
        darwin-arm64) RID=osx-arm64 ASSET="onnxruntime-osx-arm64-$VERSION.tgz" ;;
        darwin-amd64) RID="" ASSET="onnxruntime-osx-x86_64-$VERSION.tgz" ;;
        windows-amd64) RID=win-x64 ASSET="onnxruntime-win-x64-$VERSION.zip" ;;
        *) die "unsupported platform: $1" ;;
    esac
    case "$1" in
        linux-*) FILES="libonnxruntime.so libonnxruntime_providers_shared.so" ;;
        darwin-*) FILES="libonnxruntime.dylib" ;;
        windows-*) FILES="onnxruntime.dll onnxruntime_providers_shared.dll" ;;
    esac
}

# installed_name FILE: the name FILE gets in the library directory. The main
# library carries the version in its name; BirdNET-Go and the Taskfile read the
# installed version from it.
installed_name() {
    case "$1" in
        libonnxruntime.so) echo "libonnxruntime.so.$VERSION" ;;
        libonnxruntime.dylib) echo "libonnxruntime.$VERSION.dylib" ;;
        *) echo "$1" ;;
    esac
}

# links: print "LINK TARGET" for every link the loader and the linker look for.
links() {
    for f in $FILES; do
        case "$f" in
            libonnxruntime.so)
                echo "libonnxruntime.so.${VERSION%%.*} libonnxruntime.so.$VERSION"
                echo "libonnxruntime.so libonnxruntime.so.${VERSION%%.*}"
                ;;
            libonnxruntime.dylib)
                echo "libonnxruntime.dylib libonnxruntime.$VERSION.dylib"
                ;;
        esac
    done
}

# reject_strays DEST: fail when DEST holds a directory or any name install would
# not create.
reject_strays() {
    for entry in "$1"/* "$1"/.[!.]* "$1"/..?*; do
        [ -e "$entry" ] || [ -L "$entry" ] || continue
        if [ -d "$entry" ] && [ ! -L "$entry" ]; then
            die "unexpected directory in $1: ${entry##*/}. Use an empty directory"
        fi
        known=no
        for name in $(for f in $FILES; do installed_name "$f"; done) $(links | cut -d' ' -f1); do
            [ "$name" = "${entry##*/}" ] && known=yes
        done
        [ "$known" = yes ] ||
            die "unexpected entry in $1: ${entry##*/}. The directory must hold only ONNX Runtime $VERSION; use an empty one"
    done
}

# libs_verified DEST PLATFORM: succeed when DEST holds every library file as a
# regular file with its pinned hash.
libs_verified() {
    for f in $FILES; do
        inst="$1/$(installed_name "$f")"
        [ -f "$inst" ] && [ ! -L "$inst" ] || return 1
        matches_pin "$inst" "$VERSION/$2/$f" || return 1
    done
}

# links_verified DEST: succeed when every link exists and points at its target.
links_verified() {
    links | while read -r link target; do
        [ -L "$1/$link" ] && [ "$(readlink "$1/$link")" = "$target" ] || exit 1
    done
}

# fetch_archive URL NAME [LABEL]: set ARCHIVE to the archive to read: the local
# one when ONNXRUNTIME_ARCHIVE is set, else NAME in the download directory. A
# file already there is reused only when LABEL's pin vouches for it; anything
# else is downloaded again over it.
fetch_archive() {
    if [ -n "${ONNXRUNTIME_ARCHIVE:-}" ]; then
        ARCHIVE=$ONNXRUNTIME_ARCHIVE
        [ -f "$ARCHIVE" ] || die "ONNXRUNTIME_ARCHIVE not found: $ARCHIVE"
        return 0
    fi
    ARCHIVE="$DL/$2"
    if [ -n "${3:-}" ] && [ -f "$ARCHIVE" ] && matches_pin "$ARCHIVE" "$3"; then
        echo "fetch-onnxruntime: reusing $ARCHIVE" >&2
        return 0
    fi
    need curl
    echo "fetch-onnxruntime: downloading $1" >&2
    curl -fsSL --retry 3 -o "$ARCHIVE" "$1" || die "download failed: $1"
}

# open_source PLATFORM: fetch the archive that holds PLATFORM's libraries.
open_source() {
    case "$ONNXRUNTIME_SOURCE" in
        nuget)
            [ -n "$RID" ] || die "the NuGet package has no $1 build. The github source can serve it once $PINS holds pins for $VERSION/$1/"
            need unzip
            [ -n "$ARCHIVE" ] || fetch_archive "$NUGET_BASE_URL/$VERSION/$PACKAGE" "$PACKAGE" "$PACKAGE"
            ;;
        github)
            case "$ASSET" in *.zip) need unzip ;; *) need tar ;; esac
            fetch_archive "$GITHUB_BASE_URL/v$VERSION/$ASSET" "$ASSET"
            ;;
        *) die "ONNXRUNTIME_SOURCE must be nuget or github, got '$ONNXRUNTIME_SOURCE'" ;;
    esac
}

# extract FILE: write the bytes of library FILE from ARCHIVE to the download
# directory. One exactly named member is streamed, so an archive is never
# unpacked as a tree.
extract() {
    case "$ONNXRUNTIME_SOURCE" in
        nuget)
            unzip -p "$ARCHIVE" "runtimes/$RID/native/$1" >"$DL/$1" || true
            ;;
        github)
            member="${ASSET%.*}/lib/$(installed_name "$1")"
            case "$ASSET" in
                *.zip)
                    unzip -p "$ARCHIVE" "$member" >"$DL/$1" || true
                    ;;
                *)
                    # The release archives name their members with or without a
                    # leading "./".
                    found=$(tar -tzf "$ARCHIVE" | awk -v m="$member" \
                        '$0 == m || $0 == "./" m { n++; name = $0 } END { if (n == 1) print name }')
                    [ -n "$found" ] || die "expected exactly one $member in $ASSET"
                    tar -xzOf "$ARCHIVE" "$found" >"$DL/$1"
                    ;;
            esac
            ;;
    esac
    [ -s "$DL/$1" ] || die "$1 is missing or empty in the $ONNXRUNTIME_SOURCE archive"
}

check_version() {
    case "$1" in
        *[!0-9.]* | .* | *. | *..*) die "VERSION must look like 1.32.0, got '$1'" ;;
        *.*.*) ;;
        *) die "VERSION must look like 1.32.0, got '$1'" ;;
    esac
    VERSION=$1
    PACKAGE="microsoft.ml.onnxruntime.$VERSION.nupkg"
}

cmd_install() {
    [ "$#" -eq 3 ] || die "usage: $0 install VERSION PLATFORM DEST_DIR"
    check_version "$1"
    platform=$2
    dest=${3%/}
    set_platform "$platform"

    fetched=no
    if [ -d "$dest" ]; then
        reject_strays "$dest"
    fi
    if ! libs_verified "$dest" "$platform"; then
        # A sibling of DEST_DIR, so the renames below stay on one filesystem.
        DL="$dest.download"
        mkdir -p -- "$dest" "$DL"
        open_source "$platform"
        if [ "$ONNXRUNTIME_SOURCE" = nuget ]; then
            verify "$ARCHIVE" "$PACKAGE"
        fi
        for f in $FILES; do
            extract "$f"
            verify "$DL/$f" "$VERSION/$platform/$f"
        done
        for f in $FILES; do
            mv -f -- "$DL/$f" "$dest/$(installed_name "$f")"
        done
        fetched=yes
    fi

    for f in $FILES; do
        chmod "$LIB_MODE" "$dest/$(installed_name "$f")"
    done
    links | while read -r link target; do
        ln -sfn -- "$target" "$dest/$link"
    done

    # Post-condition, whichever path ran: exactly the pinned files and links.
    reject_strays "$dest"
    if ! libs_verified "$dest" "$platform" || ! links_verified "$dest"; then
        die "$dest does not hold the pinned ONNX Runtime $VERSION files after install"
    fi
    if [ "$fetched" = yes ]; then
        echo "fetch-onnxruntime: installed $VERSION for $platform from $ONNXRUNTIME_SOURCE into $dest" >&2
    else
        echo "fetch-onnxruntime: $VERSION for $platform already in $dest, verified" >&2
    fi
}

cmd_pins() {
    [ "$#" -eq 2 ] || die "usage: $0 pins VERSION DOWNLOAD_DIR"
    check_version "$1"
    DL=${2%/}
    mkdir -p -- "$DL"
    echo "# ONNX Runtime $VERSION SHA-256 pins, read by scripts/fetch-onnxruntime.sh."
    echo "# Format: sha256sum lines, \"<sha256>  <label>\". Regenerate on a version bump with"
    echo "#   sh scripts/fetch-onnxruntime.sh pins <version> <download dir> > scripts/onnxruntime-pins.sha256"
    if [ "$ONNXRUNTIME_SOURCE" = github ]; then
        [ -z "${ONNXRUNTIME_ARCHIVE:-}" ] ||
            die "pins with the github source downloads one archive per platform; unset ONNXRUNTIME_ARCHIVE"
        kept=$(pin_of "$PACKAGE")
        [ -z "$kept" ] || echo "$kept  $PACKAGE"
    fi
    for platform in $ALL_PLATFORMS; do
        set_platform "$platform"
        if [ "$ONNXRUNTIME_SOURCE" = nuget ]; then
            [ -n "$RID" ] || continue
            if [ -z "$ARCHIVE" ]; then
                open_source "$platform"
                echo "$(sha256_of "$ARCHIVE")  $PACKAGE"
            fi
        else
            open_source "$platform"
        fi
        for f in $FILES; do
            extract "$f"
            echo "$(sha256_of "$DL/$f")  $VERSION/$platform/$f"
        done
    done
}

if ! command -v sha256sum >/dev/null 2>&1; then
    need shasum
fi
cmd=${1:-}
[ "$#" -gt 0 ] && shift
case "$cmd" in
    install) cmd_install "$@" ;;
    pins) cmd_pins "$@" ;;
    *) die "usage: $0 install VERSION PLATFORM DEST_DIR | pins VERSION DOWNLOAD_DIR" ;;
esac

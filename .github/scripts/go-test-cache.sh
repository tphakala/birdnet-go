#!/usr/bin/env bash
#
# go-test-cache.sh - helpers that let the unit-tests shards in golangci-test.yml
# reuse Go's test cache across runs.
#
# Why this exists: Go replays a cached `ok` result when a test's inputs match the
# ones recorded on an earlier run. For files the test opens, those inputs are the
# file's size, mode and mtime, and a fresh checkout gives every file a new mtime,
# so nothing would ever match. This script gives every tracked file an mtime
# derived from its own content (same bytes, same mtime; any edit, even a
# same-size one, changes it), builds the cache key that carries everything Go
# cannot see (native libraries, Go version, the files that define the job), and
# prunes superseded cache entries so the repository keeps one generation per shard.
#
# Usage:
#   go-test-cache.sh key SHARD
#   go-test-cache.sh normalize
#   go-test-cache.sh self-test
#   go-test-cache.sh report EVENTS_FILE SHARD
#   go-test-cache.sh prune
#
# Subcommands:
#   key        needs RUNNER_OS, RUNNER_ARCH, ImageOS, GITHUB_RUN_ID and CI_FILES_HASH
#              in the environment. Prints key=, restore-prefix= and path= lines for
#              $GITHUB_OUTPUT; the fingerprinted files are logged to stderr.
#   normalize  sets atime and mtime of every tracked file, symlink and directory
#              (untracked paths are left alone). Run it last, right before the tests.
#   self-test  checks the properties normalize and prune rely on, in a scratch repo.
#   report     appends how many test packages were replayed from the cache to
#              $GITHUB_STEP_SUMMARY (or stdout), and logs the packages that ran.
#   prune      deletes this repository's cache entries for every shard that an
#              earlier run left behind, once this run has saved its own. Needs
#              GH_TOKEN, GITHUB_RUN_ID, GITHUB_REPOSITORY and UNIT_SHARDS.
#
# Exit code: 0 on success, 1 on a failed self-test, a missing input or an empty
# fingerprint glob. report and prune problems are warnings, not failures.

set -euo pipefail

# Prefix of every cache key this script creates. Prune only ever touches keys
# that start with it.
KEY_PREFIX=go-unit-test

# Base of the content-derived mtimes: 2001-09-09 UTC. Far enough in the past that
# Go's "file modified within 2 s" rule never applies, and no checkout produces it.
MTIME_BASE=1000000000

# Hex digits of the git blob id added to MTIME_BASE as seconds. 7 digits are 28
# bits, so file times span 2001-09 to 2010-03, and a same-size edit keeps its
# mtime only if the first 28 bits of the blob id collide (about 4e-9).
MTIME_HEX_DIGITS=7

# Directories all get one fixed mtime. That is safe because Go hashes a directory's
# listing (each entry's name and stat), not the directory's own mtime alone.
DIR_MTIME=$MTIME_BASE

# Native libraries and headers Go cannot see (it links by name, not by content).
# The setup-tensorflow action installs the TFLite library and runs
# `task check-tensorflow` for the headers; the setup-onnxruntime action installs
# the ONNX Runtime libraries. Both land where these globs look.
TFLITE_LIB_GLOB='/usr/lib/libtensorflowlite_c.so*'
ORT_LIB_GLOB='/usr/lib/libonnxruntime*.so*'
TF_HEADERS_DIR=.cache/tensorflow/tensorflow/lite

# Hex digits kept from each fingerprint (native libraries, workflow files) in the
# cache key: 48 bits, plenty to tell generations apart.
FINGERPRINT_HEX=12

# Most cache entries prune reads per shard. A shard holds one entry per main run
# until the next run prunes it, so a handful is normal.
PRUNE_LIST_LIMIT=100

# Minimum age, in seconds, a normalized file must have. Go ignores files modified
# in the last 2 s when caching, so the self-test requires a clear margin.
MIN_FILE_AGE=2

# Perl program for `normalize`. Reads `git ls-files -s -z` records on stdin and
# takes MTIME_BASE, MTIME_HEX_DIGITS and DIR_MTIME as arguments. One process does
# all the work: a `touch` per file costs seconds for a few thousand files.
NORMALIZE_PERL=$(
  cat <<'PERL'
use strict;
use warnings;
use Digest::SHA qw(sha1_hex);

my ($base, $digits, $dir_mtime) = @ARGV;
my (%dirs, @links, $files, $skipped);
$files = $skipped = 0;
$dirs{'.'} = 1;

{
  local $/ = "\0";
  while (my $rec = <STDIN>) {
    chomp $rec;
    my ($meta, $path) = split /\t/, $rec, 2;
    my ($mode) = split ' ', $meta;

    # Every directory on the way to a tracked path.
    my $d = $path;
    while ($d =~ s{/[^/]*$}{}) { $dirs{$d} = 1; }

    my $data;
    if ($mode eq '100644' || $mode eq '100755') {
      # A tracked regular file replaced by a link would make utime follow it.
      if (-l $path) {
        print STDERR "::error::go-test-cache: $path is tracked as a regular file but is a symlink\n";
        exit 1;
      }
      open(my $in, '<:raw', $path) or do { $skipped++; next; };
      $data = do { local $/; <$in> } // '';
      close $in;
    } elsif ($mode eq '120000') {
      if (!-l $path) { $skipped++; next; }
      $data = readlink($path);
      if (!defined $data) { $skipped++; next; }
    } else {
      # Gitlinks (160000) and anything else are not ours to touch.
      next;
    }

    # The git blob id of the bytes on disk, so equal bytes give equal times.
    my $id = sha1_hex('blob ' . length($data) . "\0" . $data);
    my $t = $base + hex(substr($id, 0, $digits));
    if ($mode eq '120000') {
      push @links, [$t, $path];
    } else {
      utime($t, $t, $path) or do { warn "utime $path: $!\n"; $skipped++; next; };
      $files++;
    }
  }
}

# utime follows symlinks, so links are set with touch -h.
for my $l (@links) {
  my ($t, $path) = @$l;
  system('touch', '-h', '-d', "\@$t", '--', $path) == 0
    or do { warn "touch -h $path failed\n"; $skipped++; };
}
for my $d (keys %dirs) {
  utime($dir_mtime, $dir_mtime, $d) or warn "utime $d: $!\n";
}
printf "normalized %d files, %d symlinks, %d directories (%d skipped)\n",
  $files, scalar(@links), scalar(keys %dirs), $skipped;
PERL
)

# jq program shared by `prune` and the self-test: from a `gh cache list` JSON
# array of {id, key}, print the ids of every entry except the one from the newest
# run, but only when an entry from $run itself exists (a shard that saved nothing
# in this run keeps what it has). The run id is the trailing number of the key;
# entries without one are ignored. Keeping the newest rather than $run matters
# when a newer main run finished first: an older, slower run must not delete the
# newer entry, and must not leave itself as the newest-created entry PRs restore.
# shellcheck disable=SC2016 # jq variables, not shell expansions
PRUNE_SELECT_JQ='
  ($run | tonumber) as $r
  | [ .[] | (.key | capture("-(?<n>[0-9]+)$")?) as $c
      | select($c != null)
      | . + {run: ($c.n | tonumber)} ] as $entries
  | if any($entries[]; .run == $r)
    then ($entries | map(.run) | max) as $newest
         | $entries[] | select(.run < $newest) | .id
    else empty
    end'

log() { echo "go-test-cache: $*" >&2; }

# Prefix shared by all keys of one shard, discriminators and run ids included.
shard_prefix() {
  echo "${KEY_PREFIX}-shard${1}-"
}

require_shard() {
  [[ "${1:-}" =~ ^[0-9]+$ ]] || {
    echo "::error::go-test-cache: shard must be a number, got '${1:-}'" >&2
    exit 1
  }
}

cd_repo_root() {
  cd "$(git rev-parse --show-toplevel)"
}

# select_prunable RUN_ID: reads the cache list JSON on stdin.
select_prunable() {
  jq -r --arg run "$1" "$PRUNE_SELECT_JQ"
}

# Hash the content of every file in a NUL-separated path list on stdin and print
# the first FINGERPRINT_HEX hex digits of the digest over the sorted file digests.
fingerprint() {
  LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -c1-"$FINGERPRINT_HEX"
}

cmd_key() {
  local shard=${1:-}
  require_shard "$shard"
  : "${RUNNER_OS:?}" "${RUNNER_ARCH:?}" "${ImageOS:?}" "${GITHUB_RUN_ID:?}" "${CI_FILES_HASH:?}"
  cd_repo_root

  local -a libs=()
  local glob f
  for glob in "$TFLITE_LIB_GLOB" "$ORT_LIB_GLOB"; do
    local -a matches=()
    # Unquoted on purpose: the glob is the pattern.
    # shellcheck disable=SC2206
    matches=($glob)
    if [ ! -e "${matches[0]}" ]; then
      echo "::error::go-test-cache: no file matches $glob" >&2
      exit 1
    fi
    libs+=("${matches[@]}")
  done

  local -a headers=()
  while IFS= read -r -d '' f; do
    headers+=("$f")
  done < <(find "$TF_HEADERS_DIR" -name '*.h' -type f -print0 2>/dev/null)
  if [ "${#headers[@]}" -eq 0 ]; then
    echo "::error::go-test-cache: no headers under $TF_HEADERS_DIR" >&2
    exit 1
  fi

  log "fingerprinting ${#libs[@]} libraries and ${#headers[@]} headers"
  for f in "${libs[@]}"; do log "  $f"; done

  local nat ci go_version prefix
  nat=$(printf '%s\0' "${libs[@]}" "${headers[@]}" | fingerprint)
  ci=${CI_FILES_HASH:0:FINGERPRINT_HEX}
  go_version=$(go env GOVERSION)
  prefix="$(shard_prefix "$shard")${RUNNER_OS}-${RUNNER_ARCH}-${ImageOS}-${go_version}-nat${nat}-ci${ci}-"

  echo "key=${prefix}${GITHUB_RUN_ID}"
  echo "restore-prefix=${prefix}"
  echo "path=$(go env GOCACHE)"
}

cmd_normalize() {
  cd_repo_root
  git ls-files -s -z | perl -e "$NORMALIZE_PERL" "$MTIME_BASE" "$MTIME_HEX_DIGITS" "$DIR_MTIME"
}

cmd_report() {
  local events=${1:-} shard=${2:-}
  require_shard "$shard"
  if [ ! -s "$events" ]; then
    echo "go-test-cache: no test events in '$events', nothing to report"
    return 0
  fi

  # Tested packages are those with a package-level pass or fail; replayed ones
  # printed `ok <pkg> (cached)` at package level. Packages without tests only
  # report skip, so they count as neither. A package that failed at any point is
  # never counted as replayed: a gotestsum rerun (-test.run=^Name$) can be served
  # from the cache and print `ok <pkg> (cached)` after the package had failed.
  # shellcheck disable=SC2016 # jq variables, not shell expansions
  local jq_prog='
    [ inputs | fromjson? | select(.Package != null and .Test == null) ] as $ev
    | ([ $ev[] | select(.Action == "pass" or .Action == "fail") | .Package ] | unique) as $tested
    | ([ $ev[] | select(.Action == "fail") | .Package ] | unique) as $failed
    | ([ $ev[] | select(.Action == "output" and (.Output // "" | test("^ok\\s+\\S+\\s+\\(cached\\)"))) | .Package ] | unique
        | map(select(. as $p | $failed | index($p) | not))) as $cached
    | { tested: ($tested | length),
        cached: ($tested | map(select(. as $p | $cached | index($p))) | length),
        ran: [ $ev[] | select((.Action == "pass" or .Action == "fail") and (.Package as $p | $cached | index($p) | not))
               | {pkg: .Package, elapsed: (.Elapsed // 0)} ]
              | group_by(.pkg) | map({pkg: .[0].pkg, elapsed: (map(.elapsed) | max)})
              | sort_by(-.elapsed) }'
  local result
  result=$(jq -R -n -c "$jq_prog" "$events")

  local tested cached
  tested=$(jq -r '.tested' <<<"$result")
  cached=$(jq -r '.cached' <<<"$result")
  echo "unit-tests shard ${shard}: ${cached} of ${tested} test packages replayed from the Go test cache" \
    >>"${GITHUB_STEP_SUMMARY:-/dev/stdout}"
  echo "Packages that ran (slowest first):"
  jq -r '.ran[] | "  \(.elapsed)s \(.pkg)"' <<<"$result"
}

cmd_prune() {
  : "${GH_TOKEN:?}" "${GITHUB_RUN_ID:?}" "${GITHUB_REPOSITORY:?}" "${UNIT_SHARDS:?}"
  local shard prefix listing ids id
  for ((shard = 0; shard < UNIT_SHARDS; shard++)); do
    prefix=$(shard_prefix "$shard")
    if ! listing=$(gh cache list -R "$GITHUB_REPOSITORY" --ref refs/heads/main --key "$prefix" \
      --limit "$PRUNE_LIST_LIMIT" --json id,key); then
      echo "::warning::go-test-cache: could not list caches for shard ${shard}"
      continue
    fi
    if ! jq -e --arg suffix "-${GITHUB_RUN_ID}" 'any(.[]; .key | endswith($suffix))' <<<"$listing" >/dev/null; then
      echo "shard ${shard} saved nothing in this run, keeping older entries"
      continue
    fi
    ids=$(select_prunable "$GITHUB_RUN_ID" <<<"$listing")
    if [ -z "$ids" ]; then
      echo "shard ${shard}: no superseded entries"
      continue
    fi
    while read -r id; do
      if gh cache delete "$id" -R "$GITHUB_REPOSITORY"; then
        echo "shard ${shard}: deleted superseded cache entry ${id}"
      else
        echo "::warning::go-test-cache: could not delete cache entry ${id}"
      fi
    done <<<"$ids"
  done
}

cmd_self_test() {
  local tmp
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  cd "$tmp"
  git -c init.defaultBranch=main init -q

  # sub holds only a directory, like several real tracked directories do; Go
  # hashes the child directory's mtime when a test lists the parent.
  mkdir -p sub/nested
  printf 'AAAA\n' >sub/nested/f.txt
  ln -s sub/nested/f.txt l
  ln -s sub/nested d
  printf 'untracked\n' >u.txt
  # A known, old mtime that normalize must not change.
  local untracked_mtime=1234567890
  touch -d "@${untracked_mtime}" u.txt
  git add sub/nested/f.txt l d

  cmd_normalize >/dev/null
  local m1 now
  m1=$(stat -c %Y sub/nested/f.txt)
  now=$(date +%s)
  check "tracked file mtime is at least ${MIN_FILE_AGE}s in the past" [ $((now - m1)) -ge "$MIN_FILE_AGE" ]
  check "directory holding only a directory has the fixed mtime" [ "$(stat -c %Y sub)" = "$DIR_MTIME" ]
  check "nested directory has the fixed mtime" [ "$(stat -c %Y sub/nested)" = "$DIR_MTIME" ]
  check "top directory has the fixed mtime" [ "$(stat -c %Y .)" = "$DIR_MTIME" ]

  # Same-size edit, not staged: the working tree differs from the index.
  printf 'BBBB\n' >sub/nested/f.txt
  cmd_normalize >/dev/null
  check "same-size edit changes the mtime" [ "$(stat -c %Y sub/nested/f.txt)" != "$m1" ]

  printf 'AAAA\n' >sub/nested/f.txt
  cmd_normalize >/dev/null
  check "reverted bytes give the original mtime" [ "$(stat -c %Y sub/nested/f.txt)" = "$m1" ]

  check "untracked file keeps its mtime" [ "$(stat -c %Y u.txt)" = "$untracked_mtime" ]

  # The links are normalized themselves; the paths they point at must not take
  # their time. stat without -L reports the link's own time.
  check "file symlink does not change its target" [ "$(stat -c %Y sub/nested/f.txt)" = "$m1" ]
  check "directory symlink does not change its target" [ "$(stat -c %Y sub/nested)" = "$DIR_MTIME" ]
  local link_mtime link_max=$((MTIME_BASE + 16 ** MTIME_HEX_DIGITS))
  for link_mtime in "$(stat -c %Y l)" "$(stat -c %Y d)"; do
    check "symlink mtime is not below the content-derived range" [ "$link_mtime" -ge "$MTIME_BASE" ]
    check "symlink mtime is not above the content-derived range" [ "$link_mtime" -lt "$link_max" ]
  done

  local key_prefix="go-unit-test-shard0-Linux-X64-ubuntu24-go1.27.2-natCCCCCCCCCCCC-ciBBBBBBBBBBBB-"
  local fixture
  # Runs 50, 100 and 200 plus an entry without a numeric run id.
  fixture=$(jq -n -c --arg p "$key_prefix" \
    '[{id: 1, key: ($p + "50")}, {id: 2, key: ($p + "100")}, {id: 3, key: ($p + "200")}, {id: 5, key: ($p + "x")}]')
  check "prune selects the older runs when this run saved" \
    [ "$(select_prunable 200 <<<"$fixture" | paste -sd, -)" = "1,2" ]
  # A newer main run (300) already saved: it stays, this run's entry goes.
  fixture=$(jq -n -c --arg p "$key_prefix" \
    '[{id: 2, key: ($p + "100")}, {id: 3, key: ($p + "200")}, {id: 4, key: ($p + "300")}]')
  check "prune keeps only the newest run when a newer run already saved" \
    [ "$(select_prunable 200 <<<"$fixture" | paste -sd, -)" = "2,3" ]
  check "prune selects nothing when this run saved nothing" \
    [ -z "$(select_prunable 250 <<<"$fixture")" ]

  echo "go-test-cache self-test: PASS"
}

# check DESCRIPTION TEST...: run the test words as a command; fail the self-test
# with a GitHub error annotation when it is false.
check() {
  local description=$1
  shift
  if ! "$@"; then
    echo "::error::go-test-cache self-test failed: ${description}" >&2
    exit 1
  fi
}

main() {
  local cmd=${1:-}
  [ "$#" -gt 0 ] && shift
  case "$cmd" in
    key) cmd_key "$@" ;;
    normalize) cmd_normalize ;;
    self-test) cmd_self_test ;;
    report) cmd_report "$@" ;;
    prune) cmd_prune ;;
    *)
      echo "usage: go-test-cache.sh key SHARD | normalize | self-test | report EVENTS_FILE SHARD | prune" >&2
      exit 1
      ;;
  esac
}

main "$@"

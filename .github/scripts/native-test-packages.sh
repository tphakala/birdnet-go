#!/usr/bin/env bash
#
# native-test-packages.sh - pick the Go packages a native (Windows or macOS)
# pull request run tests, instead of the whole module.
#
# Why this exists: the native jobs in windows-test.yml and macos-test.yml run on
# a slow runner with -race. Most of the suite is pure Go that the Linux shards
# already test, so a pull request run only needs what Linux cannot cover:
#   (a) packages whose Go, cgo, assembly or test file set differs from linux,
#       i.e. the packages with _windows.go, _darwin.go or similar files;
#   (b) packages at the native boundary that have tests: cgo, go-aac (SIMD
#       kernels that must execute on the runner CPU), golang.org/x/sys, and the
#       onnxruntime_go importers under internal/inference;
#   (c) when DIFF_BASE is set, the packages the pull request itself changes.
# The weekly and manual runs keep testing everything; this selection is only safe
# because that backstop exists.
#
# Usage:
#   native-test-packages.sh OUT_FILE
#
# Environment:
#   TEST_TAGS  required. The go -tags value, for example noembed,skipfrontend.
#   DIFF_BASE  optional. A git revision; when set, rule (c) selects the packages
#              touched by `git diff --name-only DIFF_BASE HEAD`. The workflows set
#              HEAD^1 on pull_request only (needs fetch-depth 2).
#   GOOS, GOARCH  optional. Default to the runner; set them to simulate another
#              platform from a Linux host.
#
# Output: OUT_FILE gets one import path per line, sorted and unique. If the diff
# cannot be read, it gets the single line ./... (fail open: test everything). The
# selected packages and the rule that picked each go to stderr.
#
# Exit code: 0 on success, 1 when go list fails or when the result would not
# exercise the native setup: it must contain internal/inference/vad, a package
# that links go-tflite and a package that links go-aac (these are added or
# required regardless of the diff, so a pull request that only changes go.mod or
# the native setup still loads the TFLite and ONNX libraries and runs the SIMD
# kernels).
#
# Portable to bash 3.2 (macOS /bin/bash): no builtin that reads lines into an
# array, no associative arrays, no case conversion. LC_ALL=C makes sort order
# the same on GNU and BSD userlands.

set -euo pipefail
export LC_ALL=C

readonly USAGE="usage: TEST_TAGS=<tags> [DIFF_BASE=<rev>] native-test-packages.sh OUT_FILE"

# Import-path fragments the rules match on.
readonly GO_AAC_PATH="github.com/tphakala/go-aac"
readonly GO_TFLITE_PATH="github.com/tphakala/go-tflite"
readonly ONNX_GO_PATH="github.com/yalue/onnxruntime_go"
readonly X_SYS_PREFIX="golang.org/x/sys/"
readonly INFERENCE_DIR="internal/inference"
readonly VAD_DIR="internal/inference/vad"
readonly WHOLE_MODULE="./..."

OUT_FILE=${1:?$USAGE}
: "${TEST_TAGS:?$USAGE}"
DIFF_BASE=${DIFF_BASE:-}

log() { printf '%s\n' "$*" >&2; }
# Windows tools can end lines with CRLF; strip it so comparisons stay exact.
strip_cr() { tr -d '\r'; }
# Set operations are done in awk, not comm: comm silently drops lines when its
# ordering and the sort ordering disagree (seen with uutils coreutils).
# only_in_second FIRST SECOND: lines of SECOND that are not lines of FIRST.
only_in_second() { awk 'NR == FNR { seen[$0] = 1; next } NF && !($0 in seen)' <(printf '%s\n' "$1") <(printf '%s\n' "$2"); }
# shared_lines FIRST SECOND: lines present in both.
shared_lines() { awk 'NR == FNR { seen[$0] = 1; next } NF && ($0 in seen)' <(printf '%s\n' "$1") <(printf '%s\n' "$2"); }

# Rule (c) input first: reading the diff is cheap, and an unreadable diff means
# test everything, whatever the rules would have chosen.
changed_files=""
if [ -n "$DIFF_BASE" ]; then
  if ! changed_files=$(git -c core.quotepath=false diff --name-only "$DIFF_BASE" HEAD | strip_cr); then
    log "warning: cannot read the diff against $DIFF_BASE, testing the whole module"
    printf '%s\n' "$WHOLE_MODULE" > "$OUT_FILE"
    exit 0
  fi
fi

# go list needs CGO_ENABLED=1 explicitly: Go turns cgo off by default when GOOS
# differs from the host, which would drop every cgo file from the comparison.
# Listing does not compile anything, so no C toolchain is needed for the linux run.
mod=$(go list -m | strip_cr)

# One line per package: import path plus the file sets compared by rule (a).
readonly SET_FORMAT='{{.ImportPath}} {{join .GoFiles ","}}|{{join .CgoFiles ","}}|{{join .TestGoFiles ","}}|{{join .XTestGoFiles ","}}|{{join .SFiles ","}}'
native_sets=$(CGO_ENABLED=1 go list -tags "$TEST_TAGS" -f "$SET_FORMAT" ./... | strip_cr | sort -u)
linux_sets=$(GOOS=linux CGO_ENABLED=1 go list -tags "$TEST_TAGS" -f "$SET_FORMAT" ./... | strip_cr | sort -u)

# One tab-separated line per package for rules (b) and (c) and the guards:
# import path, 1 if it has test files, imports of package and tests, all deps.
readonly INFO_FORMAT='{{.ImportPath}}{{"\t"}}{{if or .TestGoFiles .XTestGoFiles}}1{{else}}0{{end}}{{"\t"}}{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}{{"\t"}}{{join .Deps " "}}'
info=$(CGO_ENABLED=1 go list -tags "$TEST_TAGS" -f "$INFO_FORMAT" ./... | strip_cr)

# Rule (a): packages whose file sets exist only natively (this also catches
# packages absent on linux).
rule_a=$(only_in_second "$linux_sets" "$native_sets" |
  awk '{ print $1 "\trule a: file set differs from linux" }')

# Rule (b), plus the sets the guards need. Tags: B boundary package, T has tests
# and links go-tflite, A has tests and links go-aac, R relative dir of a package.
# internal/classifier also imports onnxruntime_go but is the slowest package, so
# the ONNX rule is limited to internal/inference.
tagged=$(awk -F'\t' -v mod="$mod" -v aac="$GO_AAC_PATH" -v tflite="$GO_TFLITE_PATH" \
  -v onnx="$ONNX_GO_PATH" -v xsys="$X_SYS_PREFIX" -v infdir="$INFERENCE_DIR" '
  function under(path, base) { return path == base || index(path, base "/") == 1 }
  {
    pkg = $1
    rel = (pkg == mod) ? "." : substr(pkg, length(mod) + 2)
    print "R\t" rel "\t" pkg
    if ($2 != 1) next
    reason = ""
    n = split($3, imps, " ")
    for (i = 1; i <= n && reason == ""; i++) {
      imp = imps[i]
      if (imp == "C") reason = "imports C (cgo)"
      else if (under(imp, aac)) reason = "imports go-aac"
      else if (index(imp, xsys) == 1) reason = "imports golang.org/x/sys"
      else if (imp == onnx && under(rel, infdir)) reason = "imports onnxruntime_go under internal/inference"
    }
    if (reason != "") print "B\t" pkg "\trule b: " reason
    m = split($4, deps, " ")
    haveT = 0; haveA = 0
    for (i = 1; i <= m; i++) {
      if (deps[i] == tflite) haveT = 1
      if (under(deps[i], aac)) haveA = 1
    }
    if (haveT) print "T\t" pkg
    if (haveA) print "A\t" pkg
  }' <<<"$info")

tflite_pkgs=$(awk -F'\t' '$1 == "T" { print $2 }' <<<"$tagged" | sort -u)
aac_pkgs=$(awk -F'\t' '$1 == "A" { print $2 }' <<<"$tagged" | sort -u)

rule_b=$(awk -F'\t' '$1 == "B" { print $2 "\t" $3 }' <<<"$tagged")

# Rule (c): map each changed path to one directory. A path under testdata/ maps
# to the directory holding it. No other walking up: a deleted package's old
# directory is no longer a package and drops out. A root-level path counts only
# when it is a .go file, so go.mod, workflow and doc changes do not select the
# root main package.
rule_c=""
if [ -n "$changed_files" ]; then
  changed_dirs=$(awk '
    NF {
      f = $0
      s = "/" f
      i = index(s, "/testdata/")
      if (i > 0) { d = substr(s, 2, i - 2); if (d != "") print d; next }
      n = split(f, parts, "/")
      if (n == 1) { if (f ~ /\.go$/) print "."; next }
      d = parts[1]
      for (j = 2; j < n; j++) d = d "/" parts[j]
      print d
    }' <<<"$changed_files" | sort -u)
  rule_c=$(awk -F'\t' 'NR == FNR { want[$0] = 1; next }
    $1 == "R" && ($2 in want) { print $3 "\trule c: pull request changes " $2 }' \
    <(printf '%s\n' "$changed_dirs") <(printf '%s\n' "$tagged"))
fi

selected=$(printf '%s\n%s\n%s\n' "$rule_a" "$rule_b" "$rule_c" | awk 'NF' | sort -u)
packages=$(printf '%s\n' "$selected" | awk -F'\t' 'NF { print $1 }' | sort -u)

# Guards: the run must exercise the native setup whatever the diff says. A run
# with no go-tflite package would never load the TFLite DLL or dylib; add one
# if the rules did not (on today's tree internal/api/v2/imports already qualifies).
if [ -z "$(shared_lines "$packages" "$tflite_pkgs")" ]; then
  extra=$(printf '%s\n' "$tflite_pkgs" | awk 'NF { print; exit }')
  if [ -z "$extra" ]; then
    log "error: no test package links go-tflite"
    exit 1
  fi
  log "guard: adding $extra (no selected package links go-tflite)"
  selected=$(printf '%s\n%s\tguard: links go-tflite\n' "$selected" "$extra" | awk 'NF' | sort -u)
  packages=$(printf '%s\n%s\n' "$packages" "$extra" | awk 'NF' | sort -u)
fi

if [ -z "$packages" ]; then
  log "error: no packages selected"
  exit 1
fi
if ! printf '%s\n' "$packages" | grep -Fxq "$mod/$VAD_DIR"; then
  log "error: $mod/$VAD_DIR is not selected (it loads the real ONNX Runtime)"
  exit 1
fi
if [ -z "$(shared_lines "$packages" "$aac_pkgs")" ]; then
  log "error: no selected package links go-aac (SIMD kernels would not run)"
  exit 1
fi

log "selected $(printf '%s\n' "$packages" | awk 'END { print NR }') packages:"
printf '%s\n' "$selected" | awk -F'\t' '{ print "  " $0 }' >&2
printf '%s\n' "$packages" > "$OUT_FILE"

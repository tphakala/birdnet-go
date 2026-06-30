#!/usr/bin/env bash
#
# ci-test-report.sh - turn one or more gotestsum --jsonfile streams into a
# compact, machine-readable failure report plus a human/LLM-friendly summary.
#
# Why this exists: raw CI logs are huge and an LLM agent (or a human) should not
# have to parse thousands of lines to learn what actually broke. This script
# emits a small `<out>-failures.json` artifact and appends a short Markdown
# section to $GITHUB_STEP_SUMMARY, classifying every test as one of:
#   - regression : failed and never passed, even after gotestsum reruns
#   - flaky      : failed at least once but passed on a rerun (NOT a code bug)
# Classification is order-independent: a test is a regression iff it has >=1
# fail event and 0 pass events; it is flaky iff it has both. This holds no
# matter how gotestsum interleaves rerun events in the JSON stream.
#
# Usage:
#   ci-test-report.sh OUT_PREFIX TITLE JSONFILE [JSONFILE...]
#
# Outputs:
#   <OUT_PREFIX>-failures.json  - JSON array of regressions (pkg, test, output)
#   appends a Markdown summary to $GITHUB_STEP_SUMMARY (or stdout if unset)
#
# Exit code: 0 always (reporting is non-fatal; the test step's own exit code
# is the gate). The caller decides what to do with the counts printed to stdout.

set -uo pipefail

if [ "$#" -lt 3 ]; then
  echo "usage: $0 OUT_PREFIX TITLE JSONFILE [JSONFILE...]" >&2
  exit 2
fi

OUT_PREFIX="$1"; shift
TITLE="$1"; shift
JSONFILES=("$@")

FAILURES_JSON="${OUT_PREFIX}-failures.json"
FLAKY_JSON="${OUT_PREFIX}-flaky.json"
SUMMARY_FILE="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

# Merge only the JSON files that exist and are non-empty, so a missing shard
# artifact does not abort the whole report.
EXISTING=()
for f in "${JSONFILES[@]}"; do
  if [ -s "$f" ]; then EXISTING+=("$f"); fi
done

if [ "${#EXISTING[@]}" -eq 0 ]; then
  echo "[]" > "$FAILURES_JSON"
  echo "[]" > "$FLAKY_JSON"
  {
    echo "### ${TITLE}"
    echo ""
    echo "No test event data found (jsonfiles missing or empty)."
    echo ""
  } >> "$SUMMARY_FILE"
  echo "regressions=0"
  echo "flaky=0"
  exit 0
fi

# Per (package, test): count fail/pass events and capture output. A regression
# never passed (passes==0); a flaky test failed then passed (passes>0, fails>0).
JQ_PROG='
  [ inputs | try fromjson catch empty | select(.Test != null) ]
  | group_by([.Package, .Test])
  | map({
      pkg:    .[0].Package,
      test:   .[0].Test,
      fails:  (map(select(.Action=="fail"))   | length),
      passes: (map(select(.Action=="pass"))   | length),
      output: (map(select(.Action=="output")) | map(.Output) | join("") )
    })
'

# -R reads each line as raw text so the per-line `try fromjson` above can skip a
# corrupt line; without -R a single bad line aborts the whole parse.
ALL=$(jq -R -n "$JQ_PROG" "${EXISTING[@]}" 2>/dev/null || echo "[]")

# here-strings instead of `echo "$ALL" |`: avoids a subshell and any echo
# backslash/leading-dash interpretation of the JSON payload.
jq '[ .[] | select(.fails > 0 and .passes == 0) | {pkg, test, output} ]' <<< "$ALL" > "$FAILURES_JSON"
jq '[ .[] | select(.fails > 0 and .passes > 0) | {pkg, test} ]' <<< "$ALL" > "$FLAKY_JSON"

REG_COUNT=$(jq 'length' "$FAILURES_JSON")
FLAKY_COUNT=$(jq 'length' "$FLAKY_JSON")

{
  echo "### ${TITLE}"
  echo ""
  if [ "$REG_COUNT" -eq 0 ] && [ "$FLAKY_COUNT" -eq 0 ]; then
    echo ":white_check_mark: All tests passed."
  else
    echo "| Outcome | Count |"
    echo "| --- | --- |"
    echo "| :x: Regressions (failed after reruns) | ${REG_COUNT} |"
    echo "| :recycle: Flaky (passed on rerun) | ${FLAKY_COUNT} |"
    echo ""
    if [ "$REG_COUNT" -gt 0 ]; then
      echo "<details><summary>Regressions - real failures, fix these</summary>"
      echo ""
      echo '```'
      jq -r '.[] | "FAIL  \(.pkg)  \(.test)"' "$FAILURES_JSON"
      echo '```'
      echo "</details>"
      echo ""
    fi
    if [ "$FLAKY_COUNT" -gt 0 ]; then
      echo "<details><summary>Flaky - failed then passed on rerun, NOT a regression</summary>"
      echo ""
      echo '```'
      jq -r '.[] | "FLAKY \(.pkg)  \(.test)"' "$FLAKY_JSON"
      echo '```'
      echo "</details>"
      echo ""
    fi
  fi
} >> "$SUMMARY_FILE"

echo "regressions=${REG_COUNT}"
echo "flaky=${FLAKY_COUNT}"
exit 0

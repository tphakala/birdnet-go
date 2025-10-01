#!/usr/bin/env bash
set -euo pipefail

# Usage: push-provider-test.sh [ok|fail]
# If first arg is "fail", exit with non-zero to test retry/handling.

MODE=${1:-ok}
LOG_FILE=${TEST_LOG_FILE:-/tmp/bng-push-script.log}

now() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }

{
  echo "---"
  echo "time: $(now)"
  echo "mode: ${MODE}"
  echo "env:" 
  echo "  NOTIFICATION_ID: ${NOTIFICATION_ID:-}"
  echo "  NOTIFICATION_TYPE: ${NOTIFICATION_TYPE:-}"
  echo "  NOTIFICATION_PRIORITY: ${NOTIFICATION_PRIORITY:-}"
  echo "  NOTIFICATION_TITLE: ${NOTIFICATION_TITLE:-}"
  echo "  NOTIFICATION_MESSAGE: ${NOTIFICATION_MESSAGE:-}"
  echo "  NOTIFICATION_COMPONENT: ${NOTIFICATION_COMPONENT:-}"
  echo "  NOTIFICATION_TIMESTAMP: ${NOTIFICATION_TIMESTAMP:-}"
  if [[ -n "${NOTIFICATION_METADATA_JSON:-}" ]]; then
    echo "  NOTIFICATION_METADATA_JSON: ${NOTIFICATION_METADATA_JSON}"
  fi

  # If JSON is provided on stdin (input_format=json or both), capture and print
  if [ -t 0 ]; then
    echo "stdin: <none>"
  else
    echo "stdin JSON:"
    cat - | sed 's/^/  /'
  fi
} >>"${LOG_FILE}" 2>&1

echo "Logged notification to ${LOG_FILE}" >&2

if [[ "${MODE}" == "fail" ]]; then
  exit 42
fi

exit 0

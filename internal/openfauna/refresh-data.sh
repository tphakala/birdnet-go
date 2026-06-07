#!/usr/bin/env bash
# Regenerates the vendored, gzipped OpenFauna dataset embedded by this package.
#
# OpenFauna (https://github.com/tphakala/openfauna) is the upstream DATA repository.
# This package embeds a committed snapshot of its compiled output. To refresh that
# snapshot after openfauna changes, run:
#
#   ./refresh-data.sh /path/to/openfauna/checkout
#
# It rebuilds the CSVs with the openfauna compiler, gzips them deterministically
# (gzip -n: no name/timestamp, so identical data produces an identical blob and a
# clean git diff) into ./data/, regenerates the locale list, and records the source
# commit in ./data/SOURCE.txt. Commit the resulting ./data/ changes.
set -euo pipefail

OF_DIR="${1:?usage: refresh-data.sh /path/to/openfauna/checkout}"
DEST="$(cd "$(dirname "$0")" && pwd)/data"
mkdir -p "$DEST"

cd "$OF_DIR"
go run ./cmd/compiler # writes build/translations.csv and build/metadata.csv
OF_SHA="$(git rev-parse --short HEAD)"
GEN_DATE="$(date -u +%Y-%m-%d)"

gzip -9 -n -c build/translations.csv >"$DEST/translations.csv.gz"
gzip -9 -n -c build/metadata.csv >"$DEST/metadata.csv.gz"
tail -n +2 build/translations.csv | cut -d, -f2 | sort -u >"$DEST/locales.txt"
printf 'openfauna@%s %s\n' "$OF_SHA" "$GEN_DATE" >"$DEST/SOURCE.txt"

echo "Refreshed openfauna data from $OF_DIR (openfauna@$OF_SHA, $GEN_DATE)"
echo "Locales: $(wc -l <"$DEST/locales.txt")"

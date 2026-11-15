#!/usr/bin/env bash
# Regenerates the vendored, gzipped OpenFauna dataset embedded by this package.
#
# OpenFauna (https://github.com/tphakala/openfauna) is the upstream DATA repository.
# This package embeds a committed snapshot of its compiled output. To refresh that
# snapshot after openfauna changes, run:
#
#   ./refresh-data.sh /path/to/openfauna/checkout
#
# Use a checkout at a release tag. It rebuilds the dataset with the openfauna
# compiler, gzips translations.csv and metadata.jsonl deterministically (gzip -n: no
# name/timestamp, so identical data produces an identical blob and a clean git diff)
# into ./data/, copies the small aliases.json verbatim, regenerates the locale list,
# records the source commit in ./data/SOURCE.txt, and regenerates the species
# dictionaries in internal/speciesdict/data from the new translations. Commit the
# resulting ./data/ and speciesdict changes.
set -euo pipefail

# Major version of the openfauna build schema this package reads (upstream asks
# consumers to gate on the major in build/manifest.json).
SUPPORTED_SCHEMA_MAJOR=2

OF_DIR="${1:?usage: refresh-data.sh /path/to/openfauna/checkout}"
DEST="$(cd "$(dirname "$0")" && pwd)/data"
REPO_ROOT="$(cd "$DEST/../../.." && pwd)"
mkdir -p "$DEST"

cd "$OF_DIR"
go run ./cmd/compiler # writes build/translations.csv and build/metadata.jsonl

# grep -o yields each "schema_version" pair on its own line (a key such as
# "min_schema_version" does not match the leading quote), and sed -n 1p reads all of
# its input, so the pipeline never ends on SIGPIPE.
SCHEMA_VERSION="$(grep -o '"schema_version"[[:space:]]*:[[:space:]]*"[^"]*"' build/manifest.json | sed -n '1s/.*"\([^"]*\)"$/\1/p')"
if [ "${SCHEMA_VERSION%%.*}" != "$SUPPORTED_SCHEMA_MAJOR" ]; then
	echo "refresh-data.sh: unsupported openfauna schema_version '${SCHEMA_VERSION}' (need major ${SUPPORTED_SCHEMA_MAJOR})" >&2
	exit 1
fi
OF_SHA="$(git rev-parse --short HEAD)"
GEN_DATE="$(date -u +%Y-%m-%d)"

gzip -9 -n -c build/translations.csv >"$DEST/translations.csv.gz"
gzip -9 -n -c build/metadata.jsonl >"$DEST/metadata.jsonl.gz"
# aliases.json (taxonomic legacy -> canonical map) is small, so it is vendored
# uncompressed and verbatim for a reviewable diff. Added in openfauna schema 2.1.0.
cp build/aliases.json "$DEST/aliases.json"
tail -n +2 build/translations.csv | cut -d, -f2 | LC_ALL=C sort -u >"$DEST/locales.txt"
printf 'openfauna@%s %s\n' "$OF_SHA" "$GEN_DATE" >"$DEST/SOURCE.txt"

# The species dictionaries are generated from the translations; CI fails when stale.
(cd "$REPO_ROOT" && go generate ./internal/speciesdict)

echo "Refreshed openfauna data from $OF_DIR (openfauna@$OF_SHA, $GEN_DATE)"
echo "Locales: $(wc -l <"$DEST/locales.txt")"

package openfauna

import _ "embed"

// The embedded artifacts are a vendored, gzipped copy of the compiled OpenFauna
// dataset (https://github.com/tphakala/openfauna). They are committed directly and
// embedded as-is, matching how this project embeds other large data assets. See
// README.md for the command that regenerates them from an openfauna checkout.

//go:embed data/translations.csv.gz
var translationsGz []byte

//go:embed data/metadata.jsonl.gz
var metadataGz []byte

//go:embed data/sources.json
var sourcesJSON []byte

//go:embed data/manifest.json
var manifestJSON []byte

//go:embed data/locales.txt
var localesList []byte

//go:embed data/SOURCE.txt
var dataSource []byte

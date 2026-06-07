# internal/openfauna

Read-only, memory-frugal lookups of species common names (translations across 40+ locales) and
taxonomic metadata, backed by a vendored, gzipped snapshot of the
[OpenFauna](https://github.com/tphakala/openfauna) dataset embedded directly into the binary.

OpenFauna is the upstream **data** repository (it maps scientific names to common names, taxonomy,
and educational links for birds and bats). This package is the birdnet-go **consumer**: openfauna
ships no Go code for lookups; that lives here.

## Consuming the package

```go
import "github.com/tphakala/birdnet-go/internal/openfauna"

// Build a sparse index for only the species you care about, in one locale.
// Memory stays proportional to your species set, not the whole dataset.
ix, err := openfauna.BuildIndex(scientificNames, "fi")
if err != nil { /* handle */ }

name, ok := ix.CommonName("Turdus merula") // "mustarastas"
meta, ok := ix.Meta("Turdus merula")       // Class/Order/Family/FamilyCommon/WikipediaURL/INaturalistURL

// For a one-off species outside your index (rare; scans the dataset, so cache it):
name, ok = openfauna.Lookup("Barbastella barbastellus", "de")

// Available locale codes (underscores, e.g. "en_uk", "zh_cn"):
codes := openfauna.Locales()

// Provenance of the embedded data (also logged when an index is built):
v := openfauna.DataVersion() // e.g. "openfauna@6a663ef 2026-06-07"
```

`BuildIndex` streams the embedded gzip once and keeps only the rows you ask for. Metadata columns
are decoded by header name, so columns added upstream (thumbnails, conservation status, etc.) do
not break this package or a shipped binary.

## Embedded data

Committed under `data/` and embedded via `go:embed` (see `embed.go`), matching how this project
embeds its other large data assets:

| File | Schema |
|---|---|
| `data/translations.csv.gz` | `scientific_name,locale,common_name` |
| `data/metadata.csv.gz` | `scientific_name,class,order,family,family_common,wikipedia_url,inaturalist_url` (expanding) |
| `data/locales.txt` | sorted distinct locale codes, one per line |
| `data/SOURCE.txt` | provenance: the openfauna commit + date the snapshot was generated from |

## Refreshing the vendored data

When openfauna changes, regenerate the snapshot from a checkout of the openfauna repo:

```bash
./refresh-data.sh /path/to/openfauna/checkout
```

This rebuilds the CSVs with openfauna's compiler, gzips them deterministically (`gzip -n`, so an
unchanged dataset yields an identical blob and a clean diff), regenerates `locales.txt`, and
records the source commit in `data/SOURCE.txt`. Commit the resulting `data/` changes.

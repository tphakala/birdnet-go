# Embedded region snapshots

These `*.regions.json` files are a build-time snapshot of the region geometry
each model family publishes on HuggingFace. They let the region resolver turn a
user's coordinates into a regional tile fully offline, and are the permanent
offline fallback the design requires. A later phase adds a remote fetch that
overrides this snapshot at runtime.

## Provenance

Do not hand-edit these files. They are generated upstream in the
`acoustic-models` repository by `scripts/perch-slice/make_regions_json.py`
(schema documented there), from the authoritative geometry in
`scripts/perch-slice/regions.py`. Every family is generated from the same
geometry, so their tile sets and tiers stay in step by construction; only the
per-tile `classes` count differs between families.

## Coverage maps (`maps/`)

`maps/<slug>.svg` are the per-region coverage maps embedded in the binary and
served by `GET /api/v2/models/regions/:slug/map`. They are generated upstream by
`scripts/perch-slice/make_coverage_maps.py` in `acoustic-models`. The geometry is
model-family-agnostic, so a tile's map is byte-identical across families; we keep
one slug-keyed set here. Do not hand-edit them. The same
`task sync-region-snapshots` refreshes them and fails loudly if a slug's map ever
differs between families (which would break the one-set-keyed-by-slug assumption).

## Refreshing

Refresh from a sibling `acoustic-models` checkout with:

```sh
task sync-region-snapshots
```

When a refresh genuinely changes the geometry (a tier reband, a new or removed
tile), update the golden tier table in `golden_tier_test.go` in the same commit
and say why in the commit message. That test is what turns an accidental change
into a CI failure rather than a silent regression.

/**
 * Species-guide numeric limits, mirrored from the backend.
 *
 * These three values were previously restated as bare literals in three places
 * (`createEmptySettings`, `settingsCoercion`, and the settings page's slider
 * bounds), so a backend change could be picked up by some of them and not others.
 * The Go originals live in `internal/conf/consts.go`
 * (`SpeciesGuideDefaultWarmTopN` / `SpeciesGuideMaxWarmTopN`) and
 * `internal/guideprovider/warm_top_n_sync_test.go` fails if the two sides drift.
 *
 * This module deliberately has no imports: it is pulled in by the settings store,
 * so anything it depended on would be initialised on that path too.
 */

/** Lowest accepted warm target. 0 disables startup warming entirely. */
export const SPECIES_GUIDE_MIN_WARM_TOP_N = 0;

/** Mirrors conf.SpeciesGuideMaxWarmTopN — the cap the backend clamps to. */
export const SPECIES_GUIDE_MAX_WARM_TOP_N = 1000;

/** Mirrors conf.SpeciesGuideDefaultWarmTopN — a fresh install's warm target. */
export const SPECIES_GUIDE_DEFAULT_WARM_TOP_N = 50;

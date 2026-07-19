// Shared, page-scoped species→color mapping so the same bird keeps one color
// across every chart on the page (who-sings-when, succession, time-of-day).
// Colors assign first-seen and are distinct only up to the 12-color palette.
// The map is reference-counted by the active charts (registerChart) and cleared
// when the last one unmounts, so it does not grow across the SPA session and
// each page view assigns colors fresh.
// Limitation: first-seen ordering. Upgrade path if >12 species must stay distinct
// on one page: seed the map from a canonical page-level species ordering.
import { speciesPalette } from './theme';
import type { ChartTheme } from './theme';

const indexBySpecies = new Map<string, number>();
// Number of mounted charts sharing the map; the map is cleared when it hits 0.
let activeChartCount = 0;

/** Stable palette color for a species (keyed by scientific name) in the current theme. */
export function getSpeciesColor(scientificName: string, theme: ChartTheme): string {
  const palette = speciesPalette(theme);
  let idx = indexBySpecies.get(scientificName);
  if (idx === undefined) {
    idx = indexBySpecies.size % palette.length;
    indexBySpecies.set(scientificName, idx);
  }
  // eslint-disable-next-line security/detect-object-injection -- idx is bounded by % palette.length
  return palette[idx];
}

/**
 * Register a chart as an active consumer of the shared color map. The map is
 * cleared when the last registered chart unmounts, so it does not grow across
 * the SPA session and each fresh page view restarts color assignment. Returns
 * the unregister function; call it on unmount, e.g. `onMount(() => registerChart())`.
 */
export function registerChart(): () => void {
  activeChartCount++;
  return () => {
    activeChartCount--;
    if (activeChartCount <= 0) {
      activeChartCount = 0;
      indexBySpecies.clear();
    }
  };
}

/** Test-only: clear the assignment map and active-chart count between cases. */
export function resetSpeciesColors(): void {
  indexBySpecies.clear();
  activeChartCount = 0;
}

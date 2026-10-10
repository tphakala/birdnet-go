/**
 * Selects the scores used by the Active Species table and its browser CSV.
 *
 * Synthetic include overrides retain score 1.0 in the API for compatibility,
 * while rangeScore carries the native geomodel probability for display.
 */
export function resolveActiveSpeciesScores(
  score: number,
  rangeScore?: number
): { displayScore: number; exportScore: number } {
  return {
    displayScore: rangeScore ?? score,
    exportScore: score,
  };
}

/** Preserves the browser CSV's historical raw-score ordering. */
export function compareActiveSpeciesForExport(
  a: { exportScore: number; exportOrder: number },
  b: { exportScore: number; exportOrder: number }
): number {
  return b.exportScore - a.exportScore || a.exportOrder - b.exportOrder;
}

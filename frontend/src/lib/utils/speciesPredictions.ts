/**
 * Shared autocomplete-prediction helpers for the settings species pickers.
 *
 * The settings pickers persist server-wide config, so the value they emit must
 * stay canonical (a server-locale common name or a scientific name the backend
 * matches against). The label shown to the visitor, by contrast, is localized to
 * the visitor's UI locale. This module is the single place that pairs a canonical
 * value with its localized label and does label-aware filtering and matching, so
 * the three picker components (SpeciesInput, EditorSpeciesInput, and
 * SpeciesListCard's inline dropdown) share one implementation instead of three.
 *
 * Invariant: a prediction's `value` is NEVER the localized label. Selecting a
 * prediction always emits `value`; the label is display-only.
 */

import { normalizeForLookup } from '$lib/utils/speciesNames';

/** A canonical value paired with the visitor-locale label shown for it. */
export interface SpeciesPrediction {
  /** Canonical value emitted/persisted (server-locale common name or scientific name). */
  value: string;
  /** Visitor-locale display label. Never persisted. */
  label: string;
}

/**
 * Pair each canonical value with its localized label.
 *
 * `localizeLabel` should resolve a canonical value to its visitor-locale name
 * (typically `v => localizeSpeciesName(scientificFor(v), v)`); when omitted or
 * when it returns nothing useful, the label falls back to the value itself so
 * non-localizable entries (e.g. taxonomy group rows) display verbatim.
 *
 * Call this inside a `$derived` so the labels recompute when the dictionary
 * store loads or the visitor switches locale.
 */
export function toLocalizedPredictions(
  values: string[],
  localizeLabel?: (value: string) => string
): SpeciesPrediction[] {
  return values.map(value => ({ value, label: localizeLabel?.(value) ?? value }));
}

/** Options for {@link filterLocalizedPredictions}. */
interface FilterOptions {
  /** Maximum number of predictions to return. Defaults to unbounded. */
  max?: number;
  /** A canonical value to exclude (e.g. the exact current input value). */
  excludeValue?: string;
}

/**
 * Filter predictions by a query, matching against BOTH the localized label and
 * the canonical value, so a visitor can type either the name they see or the
 * stored value. Comparison is NFC + case-insensitive. An empty query returns all
 * predictions (subject to `max`).
 */
export function filterLocalizedPredictions(
  predictions: SpeciesPrediction[],
  query: string,
  options: FilterOptions = {}
): SpeciesPrediction[] {
  const needle = normalizeForLookup(query);
  const exclude =
    options.excludeValue !== undefined ? normalizeForLookup(options.excludeValue) : undefined;
  const max = options.max ?? Number.POSITIVE_INFINITY;

  const out: SpeciesPrediction[] = [];
  for (const prediction of predictions) {
    const valueKey = normalizeForLookup(prediction.value);
    if (exclude !== undefined && valueKey === exclude) continue;
    if (
      needle.length === 0 ||
      normalizeForLookup(prediction.label).includes(needle) ||
      valueKey.includes(needle)
    ) {
      out.push(prediction);
      if (out.length >= max) break;
    }
  }
  return out;
}

/**
 * Resolve typed free text to a canonical value by matching it (NFC +
 * case-insensitive) against a prediction's label OR value, returning the matched
 * prediction's canonical `value`.
 *
 * Returns undefined when nothing matches, so the caller keeps the typed text
 * as-is (today's behavior for advanced raw entries). Because it only ever maps
 * text the visitor could have selected to that prediction's canonical value, it
 * can never turn a localized label into a persisted value.
 */
export function matchTypedToCanonical(
  typed: string,
  predictions: SpeciesPrediction[]
): string | undefined {
  const needle = normalizeForLookup(typed);
  if (needle.length === 0) return undefined;
  for (const prediction of predictions) {
    if (
      normalizeForLookup(prediction.label) === needle ||
      normalizeForLookup(prediction.value) === needle
    ) {
      return prediction.value;
    }
  }
  return undefined;
}

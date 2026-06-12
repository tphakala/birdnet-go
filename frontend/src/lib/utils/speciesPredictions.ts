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
  /**
   * Pre-normalized value for filtering, computed once by toLocalizedPredictions to
   * keep NFC normalization out of the per-keystroke filter loop. Optional so
   * hand-built predictions still work (the filter/match helpers fall back to
   * normalizing on demand).
   */
  normalizedValue?: string;
  /** Pre-normalized label for filtering. See normalizedValue. */
  normalizedLabel?: string;
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
  return values.map(value => {
    // Treat an empty/whitespace localized label as missing so the label always
    // falls back to the canonical value (a blank label would render nothing).
    const localized = localizeLabel?.(value);
    const label = localized && localized.trim().length > 0 ? localized : value;
    return {
      value,
      label,
      normalizedValue: normalizeForLookup(value),
      normalizedLabel: normalizeForLookup(label),
    };
  });
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
  const needle = normalizeForLookup(query.trim());
  const exclude =
    options.excludeValue !== undefined
      ? normalizeForLookup(options.excludeValue.trim())
      : undefined;
  // Clamp max to a non-negative integer and short-circuit on 0, so a zero/invalid
  // bound returns nothing instead of leaking one item past the post-push check.
  const maxRaw = options.max ?? Number.POSITIVE_INFINITY;
  const max = Number.isFinite(maxRaw) ? Math.max(0, Math.floor(maxRaw)) : Number.POSITIVE_INFINITY;
  if (max === 0) return [];

  const out: SpeciesPrediction[] = [];
  for (const prediction of predictions) {
    const valueKey = prediction.normalizedValue ?? normalizeForLookup(prediction.value);
    const labelKey = prediction.normalizedLabel ?? normalizeForLookup(prediction.label);
    // Exclude the entry the input already holds, matching either the canonical
    // value or the localized label so a fully-typed localized name does not echo.
    if (exclude !== undefined && (valueKey === exclude || labelKey === exclude)) continue;
    if (needle.length === 0 || labelKey.includes(needle) || valueKey.includes(needle)) {
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
  // Trim before normalizing so a name typed with surrounding spaces still matches
  // a prediction; otherwise the caller's fallback would persist the trimmed label
  // instead of the canonical value.
  const needle = normalizeForLookup(typed.trim());
  if (needle.length === 0) return undefined;
  for (const prediction of predictions) {
    const labelKey = prediction.normalizedLabel ?? normalizeForLookup(prediction.label);
    const valueKey = prediction.normalizedValue ?? normalizeForLookup(prediction.value);
    if (labelKey === needle || valueKey === needle) {
      return prediction.value;
    }
  }
  return undefined;
}

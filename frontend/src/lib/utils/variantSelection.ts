// Helpers for the model gallery variant picker. Dependency-light and
// unit-testable; the only non-pure import is `t` for reason localization, which
// every test environment mocks (see src/test/setup.ts).

import type { CatalogEntry, CatalogVariant, VariantReason } from '$lib/types/models';
import { t } from '$lib/i18n';

/**
 * Choose the variant the gallery preselects for an entry. This is the "smart by
 * default" decision: the server-recommended variant when present, else the
 * installed one, else the catalog default, else the first compatible one, else
 * the first variant. Returns '' when the entry has no variants.
 *
 * Each id is checked against the actual variant list so a stale recommended or
 * installed id (e.g. a variant hidden because it is Legacy) falls through to the
 * next rule instead of selecting nothing.
 */
export function pickPreselectedVariant(entry: CatalogEntry): string {
  const variants = entry.variants ?? [];
  if (variants.length === 0) return '';

  const has = (id: string | undefined): id is string =>
    id !== undefined && variants.some(v => v.id === id);

  if (has(entry.recommendedVariantId)) return entry.recommendedVariantId;
  if (has(entry.installedVariantId)) return entry.installedVariantId;

  // Prefer a compatible variant so the dialog never opens with a blocked one
  // preselected while a runnable variant exists: compatible default, else any
  // compatible, else the default, else the first. When every variant is blocked
  // the first is returned and the install control is disabled by the dialog.
  const preferred =
    variants.find(v => v.default && v.compatible) ??
    variants.find(v => v.compatible) ??
    variants.find(v => v.default) ??
    variants[0];
  return preferred.id;
}

/**
 * Map a structured reason code to its i18n key stem under
 * `analysis.gallery.reasons`. Both dot and underscore separators become
 * camelCase boundaries, so "precision.fp16_native" -> "precisionFp16Native".
 * Callers render the reason via `t(reasonKey(code), args)`. When the key has no
 * translation `t` returns the key string itself, so callers fall back to the raw
 * `code` to keep an untranslated reason inspectable (see ModelVariantPicker's
 * reasonText).
 */
export function reasonKey(code: string): string {
  const stem = code
    .split(/[._]/)
    .filter(Boolean)
    .map((segment, index) =>
      index === 0 ? segment : segment.charAt(0).toUpperCase() + segment.slice(1)
    )
    .join('');
  return `analysis.gallery.reasons.${stem}`;
}

/**
 * Localize a structured reason code, returning `fallback` when the code has no
 * translation. Unifies the two former copies of this pattern (the variant
 * picker's `reasonText`, which falls back to the raw code, and the gallery's
 * entry-level banner, which falls back to a generic localized line): each caller
 * passes the fallback string it wants. `t` returns the key itself for an unmapped
 * key, which is how "no translation" is detected.
 */
export function translateReason(
  code: string,
  args: Record<string, string> | undefined,
  fallback: string
): string {
  const key = reasonKey(code);
  const translated = t(key, args);
  return translated === key ? fallback : translated;
}

/**
 * Localize the top `limit` reasons of a recommended variant, each on the raw code
 * as fallback. The recommender orders the backend reason first (the headline) and
 * appends `region.matched` second, so rendering only the first hides the region
 * match on a recommended regional variant; surfacing the top two fixes that.
 * Returns the localized strings as an array so the caller controls layout.
 */
export function topReasons(reasons: VariantReason[] | undefined, limit = 2): string[] {
  if (!reasons?.length) return [];
  return reasons.slice(0, limit).map(r => translateReason(r.code, r.args, r.code));
}

/**
 * Human-facing label for a variant: the precision uppercased, with the part of
 * the id that is not the precision appended in parentheses so variants sharing a
 * precision stay distinguishable (e.g. "fp32" -> "FP32", "no-dft-fp32" ->
 * "FP32 (no-dft)", "int8-arm" -> "INT8 (arm)"). Falls back to the raw id when the
 * variant carries no precision. The region, when set, is appended last.
 */
// Variant-id delimiters: "<precision>[-<descriptor>]" optionally suffixed with
// "@<region>" for a regional tile (e.g. "int8-arm@nordic").
const VARIANT_REGION_SEPARATOR = '@';
const VARIANT_DESCRIPTOR_SEPARATOR = /[-_]/;

export function variantLabel(variant: CatalogVariant): string {
  const precision = variant.precision?.toUpperCase() ?? '';
  // Derive the non-precision descriptor from the id. Strip any "@region" suffix
  // first (the region is appended separately) so a regional id like
  // "int8-arm@nordic" yields the "arm" descriptor, not "arm@nordic".
  const baseId = variant.id.split(VARIANT_REGION_SEPARATOR)[0];
  const extra = baseId
    .split(VARIANT_DESCRIPTOR_SEPARATOR)
    .filter(segment => segment !== '' && segment.toLowerCase() !== variant.precision?.toLowerCase())
    .join('-');
  let base = precision || baseId;
  if (precision && extra) {
    base = `${precision} (${extra})`;
  }
  // The region is shown as its slug. The canonical localized region names live in
  // the region selector / regions endpoint, not here; surfacing those in the
  // variant label is a follow-up (would need the slug->name map threaded in).
  return variant.region ? `${base} (${variant.region})` : base;
}

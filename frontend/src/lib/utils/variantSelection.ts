// Pure helpers for the model gallery variant picker. No Svelte, no I/O, so they
// are trivially unit-testable.

import type { CatalogEntry, CatalogVariant } from '$lib/types/models';

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
 * Human-facing label for a variant: the precision uppercased, with the part of
 * the id that is not the precision appended in parentheses so variants sharing a
 * precision stay distinguishable (e.g. "fp32" -> "FP32", "no-dft-fp32" ->
 * "FP32 (no-dft)", "int8-arm" -> "INT8 (arm)"). Falls back to the raw id when the
 * variant carries no precision. The region, when set, is appended last.
 */
export function variantLabel(variant: CatalogVariant): string {
  const precision = variant.precision?.toUpperCase() ?? '';
  const extra = variant.id
    .split(/[-_]/)
    .filter(segment => segment !== '' && segment.toLowerCase() !== variant.precision?.toLowerCase())
    .join('-');
  let base = precision || variant.id;
  if (precision && extra) {
    base = `${precision} (${extra})`;
  }
  return variant.region ? `${base} (${variant.region})` : base;
}

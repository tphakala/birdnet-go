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

export function variantLabel(
  variant: CatalogVariant,
  regionNames?: ReadonlyMap<string, string>
): string {
  // The embedded baseline carries no precision or descriptive id ("builtin"), so
  // give it its own localized label rather than showing the raw id.
  if (variant.builtIn) return t('analysis.gallery.builtIn');
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
  if (!variant.region) return base;
  // Resolve the region's canonical display name from the same source the region
  // selector uses (the regions endpoint, threaded in as a slug->name map). Fall
  // back to the raw slug when the map is absent or the slug is unknown, so the
  // label never shows a bare slug when a name is available, and never breaks when
  // it is not.
  const regionDisplay = regionNames?.get(variant.region) ?? variant.region;
  return `${base} (${regionDisplay})`;
}

/**
 * The hardware-class token vocabulary, kept in lockstep with the server's
 * authoritative set (`internal/api/v2/models/models.go` variantHardwareClass) and
 * used as the `analysis.gallery.hardware.<token>` i18n key suffix. Centralized so the
 * mapper, the label resolver, and their tests reference one source of truth rather
 * than repeating the raw token strings. `builtIn` is the embedded baseline.
 */
export const HARDWARE_CLASS = {
  builtIn: 'builtIn',
  gpuNvidia: 'gpuNvidia',
  gpuIntel: 'gpuIntel',
  amd64Cpu: 'amd64Cpu',
  arm64Cpu: 'arm64Cpu',
  armCpu: 'armCpu',
  cpu: 'cpu',
} as const;

/**
 * A hardware-class token: either server-emitted (any of HARDWARE_CLASS) or from the
 * client-side fallback below. The fallback emits only the subset it can derive from
 * the request: it cannot make the CPU arch explicit, so it never returns
 * amd64Cpu/arm64Cpu. Both paths resolve the same `analysis.gallery.hardware.<token>`
 * key set.
 */
export type VariantHardwareClass = (typeof HARDWARE_CLASS)[keyof typeof HARDWARE_CLASS];

/**
 * The recommended (or otherwise chosen) execution backend token for a variant,
 * taken from its recommendation reasons. Prefers the `backend.recommended` reason,
 * then any reason carrying a `backend` arg. Returns undefined when the request was
 * not eligible for recommendations (no reasons), so callers fall back to id parsing.
 */
function chosenBackendToken(variant: CatalogVariant): string | undefined {
  const reasons = variant.reasons ?? [];
  const recommended = reasons.find(r => r.code === 'backend.recommended' && r.args?.backend);
  if (recommended?.args?.backend) return recommended.args.backend;
  const anyBackend = reasons.find(r => r.args?.backend);
  return anyBackend?.args?.backend;
}

/**
 * Derive a friendly hardware class for a variant entirely client-side. The BuiltIn
 * baseline is its own class. Otherwise the chosen backend token decides: a CUDA or
 * TensorRT path is a discrete GPU, an OpenVINO GPU path is an Intel GPU. When no
 * backend token is available (an unauthenticated request carries no reasons), the
 * variant id is the fallback: an "arm" descriptor marks an ARM CPU build, everything
 * else is a generic CPU. Precision alone is NOT used: an INT8 build is not
 * necessarily ARM (a future x86 INT8 variant would be mislabeled), so only the
 * explicit "arm" token in the id classifies as ARM.
 */
export function variantHardwareClass(variant: CatalogVariant): VariantHardwareClass {
  if (variant.builtIn) return HARDWARE_CLASS.builtIn;
  const backend = chosenBackendToken(variant);
  if (backend === 'cuda' || backend === 'tensorrt') return HARDWARE_CLASS.gpuNvidia;
  if (backend === 'openvino-gpu') return HARDWARE_CLASS.gpuIntel;
  if (variant.id.toLowerCase().includes('arm')) return HARDWARE_CLASS.armCpu;
  return HARDWARE_CLASS.cpu;
}

/**
 * Localized hardware chip label for a variant, the plain-language answer to "which
 * hardware is this build for" that replaces raw precision (FP16/FP32) as the primary
 * label. Prefers the server-computed `hardwareClass` token, which is authoritative
 * because it is derived from the live host architecture and the chosen backend (so a
 * CPU build reads "AMD64 CPU" or "ARM64 CPU" as appropriate); falls back to the
 * coarser client-side class when an older server omits the token. Either token maps
 * to `analysis.gallery.hardware.<token>`, except the built-in baseline.
 */
export function variantHardwareLabel(variant: CatalogVariant): string {
  // The server omits hardwareClass (omitempty) rather than sending "", so nullish
  // coalescing is the correct fallback: only an absent token falls through to the
  // coarser client-side class.
  const cls = variant.hardwareClass ?? variantHardwareClass(variant);
  if (cls === HARDWARE_CLASS.builtIn) return t('analysis.gallery.builtIn');
  return t(`analysis.gallery.hardware.${cls}`);
}

/**
 * A within-model "optimize" offer: an installed model whose recommended variant for
 * this host differs from the installed one and is compatible. `from` is the
 * installed variant, `to` the recommended one, and `reasons` the localized headline
 * reasons for the recommendation. Offers are always same-model (a better build of a
 * model the user already has), never cross-model.
 */
export interface OptimizeOffer {
  entry: CatalogEntry;
  /**
   * The installed variant being replaced, or null when the installed variant id is
   * no longer in the catalog (a build deprecated and dropped). The offer still
   * surfaces in that case, precisely so the user can move off the dead variant; only
   * the "from" label is unavailable.
   */
  from: CatalogVariant | null;
  to: CatalogVariant;
  reasons: string[];
}

/**
 * Derive the within-model optimize offers from the catalog, entirely client-side.
 * An entry qualifies when it is installed, carries variants, its installed variant
 * differs from the host-recommended variant, and that recommended variant is
 * compatible and present in the list. The permanent BirdNET v2.4 model participates
 * like any other installed model (its BuiltIn baseline is the installed variant when
 * no DFT build is active).
 */
export function optimizeOffers(catalog: CatalogEntry[]): OptimizeOffer[] {
  const offers: OptimizeOffer[] = [];
  for (const entry of catalog) {
    const variants = entry.variants ?? [];
    if (variants.length === 0 || !entry.installed) continue;

    const installedId = entry.installedVariantId;
    const recommendedId = entry.recommendedVariantId;
    if (!installedId || !recommendedId || installedId === recommendedId) continue;

    const to = variants.find(v => v.id === recommendedId);
    if (!to?.compatible) continue;
    // `from` may be absent when the installed variant was dropped from the catalog;
    // the offer still stands (move off the dead variant onto the recommendation).
    const from = variants.find(v => v.id === installedId) ?? null;

    offers.push({ entry, from, to, reasons: topReasons(to.reasons) });
  }
  return offers;
}

/**
 * The catalog release channel marking a developer-preview (not-GA) build. The gallery
 * flags an entry on this channel with a PREVIEW badge and a not-GA notice. Mirrors the
 * Go `classifier.ChannelPreview` constant.
 */
export const CHANNEL_PREVIEW = 'preview';

/**
 * The catalog release channel marking a stable (GA) build, the default an entry
 * without an explicit channel resolves to. Mirrors the Go `classifier.ChannelStable`
 * constant.
 */
export const CHANNEL_STABLE = 'stable';

/**
 * The canonical "automatic" region mode. An empty string, null, and undefined
 * all mean automatic in the gallery, mirroring the Go `ModelRegion` field's
 * `omitempty` (an unset region is omitted from JSON entirely).
 */
export const DEFAULT_REGION_MODE = 'auto';

/**
 * The explicit "global" region mode: force the location-independent global
 * variant regardless of the configured location. Distinct from
 * DEFAULT_REGION_MODE ('auto', which resolves from the location) and from a
 * concrete region slug. Mirrors the Go `ModelRegionGlobal` sentinel.
 */
export const GLOBAL_REGION_MODE = 'global';

/**
 * Normalize a stored region mode to its canonical form: '', null and undefined
 * all collapse to 'auto', while a concrete slug or 'global' passes through
 * unchanged. Centralizing this keeps the live/saved region derivations and the
 * settings dirty-check from drifting apart (comparing a raw '' against a
 * user-selected 'auto' would otherwise flag a logical no-op as an edit).
 */
export function normalizeRegionMode(value: string | undefined | null): string {
  // Intentional falsy check: '' is a valid "automatic" sentinel too, not just
  // null/undefined, so it must collapse to DEFAULT_REGION_MODE as well.
  // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- '' also means automatic
  return value || DEFAULT_REGION_MODE;
}

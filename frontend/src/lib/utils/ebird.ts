/**
 * Helpers for building links to eBird species pages.
 *
 * eBird species URLs follow the shape:
 *   https://ebird.org/species/{speciesCode}/{optionalRegion}?siteLanguage={lang}
 *
 * The species code comes from detection data, the optional region from user
 * settings, and the site language from the current BirdNET-Go UI locale.
 */

/** Base URL for an eBird species page (no trailing slash). */
export const EBIRD_SPECIES_BASE_URL = 'https://ebird.org/species';

/** Query parameter eBird uses to select the page language. */
export const EBIRD_SITE_LANGUAGE_PARAM = 'siteLanguage';

/**
 * Matches valid eBird species codes (lowercase letters/digits, e.g. "euptit1",
 * "amerob"). BirdNET-Go generates placeholder codes with an uppercase prefix for
 * species missing from the taxonomy; those are intentionally excluded so the
 * link only appears for real eBird codes.
 */
const EBIRD_SPECIES_CODE_PATTERN = /^[a-z][a-z0-9]*$/;

/**
 * Matches eBird region codes: a two-letter country (e.g. "BE", "US"),
 * optionally followed by one or two subdivision segments
 * (e.g. "BE-WAL", "US-NY-109"). Matched case-insensitively.
 */
// Bounded on every quantifier and each segment is anchored by a literal "-",
// so there is no ambiguous overlap and no catastrophic backtracking risk.
// eslint-disable-next-line security/detect-unsafe-regex -- fully bounded, linear-time pattern
const EBIRD_REGION_CODE_PATTERN = /^[a-z]{2}(?:-[a-z0-9]{1,12}){0,2}$/i;

/** Returns true when the code looks like a real (non-placeholder) eBird species code. */
export function isValidEbirdSpeciesCode(code: string | null | undefined): boolean {
  if (!code) return false;
  return EBIRD_SPECIES_CODE_PATTERN.test(code.trim());
}

/** Returns true when the region is empty (global) or a valid eBird region code. */
export function isValidEbirdRegionCode(region: string | null | undefined): boolean {
  const trimmed = region?.trim() ?? '';
  if (trimmed === '') return true;
  return EBIRD_REGION_CODE_PATTERN.test(trimmed);
}

/**
 * Reduces a BirdNET-Go UI locale to the base language code eBird expects for
 * its siteLanguage parameter (e.g. "fr-BE" -> "fr", "pt_BR" -> "pt").
 */
export function toEbirdSiteLanguage(locale: string | null | undefined): string {
  if (!locale) return '';
  const base = locale.trim().split(/[-_]/)[0] ?? '';
  return base.toLowerCase();
}

interface EbirdSpeciesUrlParams {
  speciesCode: string | null | undefined;
  /** Optional eBird region code; empty/invalid falls back to the global page. */
  region?: string | null;
  /** Current UI locale, reduced to an eBird site language. */
  locale?: string | null;
}

/**
 * Builds the eBird species page URL for the given species code, region and
 * locale. Returns null when no valid eBird species code is available, so callers
 * can hide the link entirely.
 */
export function buildEbirdSpeciesUrl({
  speciesCode,
  region,
  locale,
}: EbirdSpeciesUrlParams): string | null {
  const code = speciesCode?.trim() ?? '';
  if (!isValidEbirdSpeciesCode(code)) return null;

  const trimmedRegion = region?.trim() ?? '';
  const useRegion = trimmedRegion !== '' && isValidEbirdRegionCode(trimmedRegion);

  const path = useRegion
    ? `${EBIRD_SPECIES_BASE_URL}/${encodeURIComponent(code)}/${encodeURIComponent(trimmedRegion)}`
    : `${EBIRD_SPECIES_BASE_URL}/${encodeURIComponent(code)}`;

  const url = new URL(path);
  const siteLanguage = toEbirdSiteLanguage(locale);
  if (siteLanguage) {
    url.searchParams.set(EBIRD_SITE_LANGUAGE_PARAM, siteLanguage);
  }
  return url.toString();
}

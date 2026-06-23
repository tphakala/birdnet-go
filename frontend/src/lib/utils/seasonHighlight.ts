/**
 * seasonHighlight maps a backend `current_season` token to display metadata for
 * the species guide season badge. The backend (computeCurrentSeason) emits
 * hemisphere-aware tokens: spring/summer/autumn/winter, plus wet1/dry1/wet2/dry2
 * for the equatorial band. This module is pure and self-contained.
 */

export type Hemisphere = 'northern' | 'southern' | 'equatorial';

/** Latitude band (degrees) treated as equatorial (bimodal wet/dry seasons). */
const EQUATORIAL_BAND = 10;

/** hemisphereForLatitude classifies a latitude into a hemisphere band. */
export function hemisphereForLatitude(latitude: number): Hemisphere {
  if (latitude <= EQUATORIAL_BAND && latitude >= -EQUATORIAL_BAND) {
    return 'equatorial';
  }
  return latitude >= 0 ? 'northern' : 'southern';
}

/**
 * Stable lucide icon identifier per season token. The badge renders the matching
 * `@lucide/svelte` icon, keeping the season badge consistent with the rest of the
 * UI's icon system (and avoiding cross-platform emoji-font rendering differences).
 * A Map so the server-provided season token is looked up without indexing a plain
 * object by external input.
 */
export type SeasonIcon = 'sprout' | 'sun' | 'leaf' | 'snowflake' | 'cloud-rain' | 'sun-dim';

const SEASON_ICON = new Map<string, SeasonIcon>([
  ['spring', 'sprout'],
  ['summer', 'sun'],
  ['autumn', 'leaf'],
  ['winter', 'snowflake'],
  ['wet1', 'cloud-rain'],
  ['wet2', 'cloud-rain'],
  ['dry1', 'sun-dim'],
  ['dry2', 'sun-dim'],
]);

export interface SeasonHighlight {
  /** Normalized season token. */
  token: string;
  /** i18n key for the localized season label. */
  i18nKey: string;
  /** Lucide icon identifier, or null when the token is unknown. */
  icon: SeasonIcon | null;
  /** Whether this is an equatorial wet/dry season token. */
  isEquatorial: boolean;
}

/**
 * getSeasonHighlight returns display metadata for a `current_season` token, or
 * null when the token is empty/undefined.
 */
export function getSeasonHighlight(
  currentSeason: string | undefined | null
): SeasonHighlight | null {
  if (!currentSeason) return null;
  const token = currentSeason.trim().toLowerCase();
  if (!token) return null;
  return {
    token,
    i18nKey: `analytics.species.guide.season.${token}`,
    icon: SEASON_ICON.get(token) ?? null,
    isEquatorial: token.startsWith('wet') || token.startsWith('dry'),
  };
}

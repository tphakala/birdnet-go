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

/** Decorative emoji per season token (plain text, not an inline SVG). */
const SEASON_EMOJI: Record<string, string> = {
  spring: '🌱',
  summer: '☀️',
  autumn: '🍂',
  winter: '❄️',
  wet1: '🌧️',
  wet2: '🌧️',
  dry1: '🌵',
  dry2: '🌵',
};

export interface SeasonHighlight {
  /** Normalized season token. */
  token: string;
  /** i18n key for the localized season label. */
  i18nKey: string;
  /** Decorative emoji, or '' when the token is unknown. */
  emoji: string;
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
    emoji: SEASON_EMOJI[token] ?? '',
    isEquatorial: token.startsWith('wet') || token.startsWith('dry'),
  };
}

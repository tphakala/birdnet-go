/**
 * seasonHighlight maps a backend `current_season` token to display metadata for
 * the species guide season badge. The backend (computeCurrentSeason) emits
 * hemisphere-aware tokens: spring/summer/autumn/winter, plus wet1/dry1/wet2/dry2
 * for the equatorial band. This module is pure and self-contained.
 *
 * The hemisphere/equatorial-band classification deliberately lives only on the
 * backend, which is the authority: the token arrives already resolved, so a copy
 * of that logic here could only ever drift out of agreement with it.
 */

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
  /** i18n key for the localized season label. */
  i18nKey: string;
  /** Lucide icon identifier, or null when the token is unknown. */
  icon: SeasonIcon | null;
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
    i18nKey: `analytics.species.guide.season.${token}`,
    icon: SEASON_ICON.get(token) ?? null,
  };
}

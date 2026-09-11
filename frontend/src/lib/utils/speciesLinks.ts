const ALL_ABOUT_BIRDS_GUIDE_URL = 'https://www.allaboutbirds.org/guide/';
const ENGLISH_WIKIPEDIA_LANGUAGE = 'en';
const WIKIPEDIA_LANGUAGE_CODES: Record<string, string> = {
  nb: 'no',
};

/**
 * Build an All About Birds guide URL from a species common name.
 * Apostrophes are removed to match the site's guide URL convention.
 */
export function getAllAboutBirdsUrl(commonName: string): string {
  const guideName = commonName
    .trim()
    .replace(/[\u0027\u2019]/g, '')
    .replace(/\s+/g, '_');
  return `${ALL_ABOUT_BIRDS_GUIDE_URL}${encodeURIComponent(guideName)}/id`;
}

/**
 * Build a Wikipedia URL using a localized name when one is available.
 * When the display name is the English fallback, use English Wikipedia rather
 * than a localized host where that fallback name may not have an article.
 * Norwegian Bokmal (`nb`) maps to Wikipedia's `no` domain.
 */
export function getWikipediaUrl(
  displayName: string,
  locale: string,
  englishName: string = displayName
): string {
  const hasLocalizedName = displayName.trim() !== englishName.trim();
  const language = hasLocalizedName
    ? (WIKIPEDIA_LANGUAGE_CODES[locale] ?? locale)
    : ENGLISH_WIKIPEDIA_LANGUAGE;
  const articleName = (hasLocalizedName ? displayName : englishName).trim().replace(/\s+/g, '_');
  return `https://${language}.wikipedia.org/wiki/${encodeURIComponent(articleName)}`;
}

const ALL_ABOUT_BIRDS_GUIDE_URL = 'https://www.allaboutbirds.org/guide/';
const ENGLISH_WIKIPEDIA_LANGUAGE = 'en';
const WIKIPEDIA_LANGUAGE_CODES: Record<string, string> = {
  nb: 'no',
};

/**
 * Build the All About Birds guide slug from a species common name.
 * Apostrophes are removed to match the site's guide URL convention.
 */
function buildAllAboutBirdsGuideName(commonName: string): string {
  return commonName.trim().replace(/['’]/g, '').replace(/\s+/g, '_');
}

function buildAllAboutBirdsUrl(commonName: string, page: 'id' | 'sounds'): string {
  const guideName = buildAllAboutBirdsGuideName(commonName);
  return `${ALL_ABOUT_BIRDS_GUIDE_URL}${encodeURIComponent(guideName)}/${page}`;
}

/**
 * Build an All About Birds identification guide URL from a species common name.
 */
export function getAllAboutBirdsUrl(commonName: string): string {
  return buildAllAboutBirdsUrl(commonName, 'id');
}

/**
 * Build an All About Birds "Sounds" page URL from a species common name
 * (e.g. `https://www.allaboutbirds.org/guide/Black-throated_Blue_Warbler/sounds`),
 * for comparing a recorded detection against reference song/call recordings.
 */
export function getAllAboutBirdsSoundsUrl(commonName: string): string {
  return buildAllAboutBirdsUrl(commonName, 'sounds');
}

/**
 * Build a Wikipedia URL using a localized name when one is available.
 * When the display name is the English fallback, use English Wikipedia rather
 * than a localized host where that fallback name may not have an article.
 * Norwegian Bokmal (`nb`) maps to Wikipedia's `no` domain.
 */
function localizedWikipediaLanguage(locale: string): string {
  // eslint-disable-next-line security/detect-object-injection -- Safe: read-only lookup in a small fixed Record, falls back to the locale itself
  return WIKIPEDIA_LANGUAGE_CODES[locale] ?? locale;
}

export function getWikipediaUrl(
  displayName: string,
  locale: string,
  englishName: string = displayName
): string {
  const hasLocalizedName = displayName.trim() !== englishName.trim();
  const language = hasLocalizedName
    ? localizedWikipediaLanguage(locale)
    : ENGLISH_WIKIPEDIA_LANGUAGE;
  const articleName = (hasLocalizedName ? displayName : englishName).trim().replace(/\s+/g, '_');
  return `https://${language}.wikipedia.org/wiki/${encodeURIComponent(articleName)}`;
}

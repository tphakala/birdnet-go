const ALL_ABOUT_BIRDS_GUIDE_URL = 'https://www.allaboutbirds.org/guide/';
const WIKIPEDIA_LANGUAGE_CODES: Record<string, string> = {
  nb: 'no',
};

export function getAllAboutBirdsUrl(commonName: string): string {
  const guideName = commonName
    .trim()
    .replace(/[\u0027\u2019]/g, '')
    .replace(/\s+/g, '_');
  return `${ALL_ABOUT_BIRDS_GUIDE_URL}${encodeURIComponent(guideName)}/id`;
}

export function getWikipediaUrl(displayName: string, locale: string): string {
  const language = WIKIPEDIA_LANGUAGE_CODES[locale] ?? locale;
  const articleName = displayName.trim().replace(/\s+/g, '_');
  return `https://${language}.wikipedia.org/wiki/${encodeURIComponent(articleName)}`;
}

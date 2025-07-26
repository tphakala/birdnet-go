import { DEFAULT_LOCALE, isValidLocale, type Locale } from './config.js';

/**
 * Extract locale from URL pathname
 */
export function extractLocaleFromPathname(pathname: string): Locale {
  const segments = pathname.split('/').filter(Boolean);
  const firstSegment = segments[0];

  if (firstSegment && isValidLocale(firstSegment)) {
    return firstSegment;
  }

  return DEFAULT_LOCALE;
}

/**
 * Remove locale prefix from pathname
 */
export function removeLocaleFromPathname(pathname: string): string {
  const segments = pathname.split('/').filter(Boolean);
  const firstSegment = segments[0];

  if (firstSegment && isValidLocale(firstSegment)) {
    return '/' + segments.slice(1).join('/');
  }

  return pathname;
}

/**
 * Add locale prefix to pathname
 */
export function addLocaleToPathname(pathname: string, locale: Locale): string {
  // Remove any existing locale first
  const cleanPathname = removeLocaleFromPathname(pathname);

  // Don't add prefix for default locale
  if (locale === DEFAULT_LOCALE) {
    return cleanPathname;
  }

  // Add locale prefix
  return `/${locale}${cleanPathname}`;
}

/**
 * Create a localized URL from current URL and target locale
 */
export function localizeUrl(currentUrl: URL, targetLocale: Locale): URL {
  const newUrl = new URL(currentUrl);
  newUrl.pathname = addLocaleToPathname(currentUrl.pathname, targetLocale);
  return newUrl;
}

/**
 * Parse Accept-Language header and find best matching supported locale
 */
export function getBrowserPreferredLocale(acceptLanguageHeader: string | null): Locale {
  if (!acceptLanguageHeader) {
    return DEFAULT_LOCALE;
  }

  // Parse Accept-Language header (e.g., "en-US,en;q=0.9,de;q=0.8,fr;q=0.7")
  const languages = acceptLanguageHeader
    .split(',')
    .map(lang => {
      const trimmed = lang.trim();
      if (!trimmed) return null;

      // Split on first semicolon to separate language from parameters
      const semicolonIndex = trimmed.indexOf(';');
      let code: string;
      let quality = 1.0;

      if (semicolonIndex === -1) {
        // No parameters, just the language code
        code = trimmed;
      } else {
        // Extract language code and parse parameters
        code = trimmed.substring(0, semicolonIndex).trim();
        const params = trimmed.substring(semicolonIndex + 1);

        // Look for quality parameter (q=value)
        const qMatch = params.match(/(?:^|[;&\s])q\s*=\s*([0-9]*\.?[0-9]+)/i);
        if (qMatch) {
          const parsedQ = parseFloat(qMatch[1]);
          // Ensure quality is within valid range [0, 1]
          quality = !isNaN(parsedQ) && parsedQ >= 0 && parsedQ <= 1 ? parsedQ : 1.0;
        }
      }

      // Extract primary language code (e.g., "en-US" -> "en")
      const primaryCode = code.toLowerCase().split('-')[0].trim();

      // Validate that we have a non-empty language code
      return primaryCode ? { code: primaryCode, quality } : null;
    })
    .filter(Boolean) // Remove null entries from malformed language tags
    .sort((a, b) => b.quality - a.quality); // Sort by quality (preference)

  // Find first matching supported locale
  for (const { code } of languages) {
    if (isValidLocale(code)) {
      return code;
    }
  }

  return DEFAULT_LOCALE;
}

/**
 * Detect preferred locale from browser navigator languages
 */
export function detectBrowserLocale(): Locale {
  if (typeof navigator === 'undefined') {
    return DEFAULT_LOCALE;
  }

  // Get browser languages in order of preference
  const browserLanguages = navigator.languages || [navigator.language || DEFAULT_LOCALE];

  // Find first matching supported locale
  for (const lang of browserLanguages) {
    // Extract primary language code (e.g., "en-US" -> "en")
    const primaryCode = lang.toLowerCase().split('-')[0];
    if (isValidLocale(primaryCode)) {
      return primaryCode;
    }
  }

  return DEFAULT_LOCALE;
}

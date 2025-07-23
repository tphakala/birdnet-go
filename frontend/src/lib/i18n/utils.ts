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
      const [code, qValue] = lang.trim().split(';q=');
      const quality = qValue ? parseFloat(qValue) : 1.0;
      // Extract primary language code (e.g., "en-US" -> "en")
      const primaryCode = code.toLowerCase().split('-')[0];
      return { code: primaryCode, quality };
    })
    .sort((a, b) => b.quality - a.quality); // Sort by quality (preference)

  // Find first matching supported locale
  for (const { code } of languages) {
    if (isValidLocale(code)) {
      return code;
    }
  }

  return DEFAULT_LOCALE;
}

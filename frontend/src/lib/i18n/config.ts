/**
 * Centralized i18n configuration
 * This is the single source of truth for all locale information
 */

export const LOCALES = {
  en: { name: 'English', flag: '🇺🇸' },
  de: { name: 'Deutsch', flag: '🇩🇪' },
  fr: { name: 'Français', flag: '🇫🇷' },
  es: { name: 'Español', flag: '🇪🇸' },
  fi: { name: 'Suomi', flag: '🇫🇮' },
  pt: { name: 'Português', flag: '🇵🇹' },
} as const;

export type Locale = keyof typeof LOCALES;
export const DEFAULT_LOCALE: Locale = 'en';
export const LOCALE_CODES = Object.keys(LOCALES) as Locale[];

/**
 * Check if a string is a valid locale code
 */
export function isValidLocale(locale: string): locale is Locale {
  return LOCALE_CODES.includes(locale as Locale);
}

/**
 * Get locale info by code
 */
export function getLocaleInfo(locale: Locale) {
  // Safe access since locale is typed as Locale
  // eslint-disable-next-line security/detect-object-injection
  return LOCALES[locale];
}

/**
 * Get non-default locales (used for routing)
 */
export const NON_DEFAULT_LOCALES = LOCALE_CODES.filter(code => code !== DEFAULT_LOCALE);

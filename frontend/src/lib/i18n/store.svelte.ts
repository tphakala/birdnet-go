/* eslint-disable no-undef */
import { DEFAULT_LOCALE, type Locale, isValidLocale } from './config.js';
// Note: Type imports will be used when type-safe translation is implemented
// import type { TranslationKey, GetParams } from './types.generated.js';

// Initialize locale from localStorage or use default
function getInitialLocale(): Locale {
  if (typeof localStorage !== 'undefined') {
    const stored = localStorage.getItem('birdnet-locale');
    if (stored && isValidLocale(stored)) {
      return stored;
    }
  }
  return DEFAULT_LOCALE;
}

// State management with Svelte 5 runes
let currentLocale = $state<Locale>(getInitialLocale());
let messages = $state<Record<string, string>>({});
let loading = $state(false);
/* eslint-enable no-undef */

/**
 * Get the current locale
 * @returns The current locale code
 */
export function getLocale(): Locale {
  return currentLocale;
}

/**
 * Set the current locale and load corresponding messages
 * @param locale - The locale code to set
 */
export function setLocale(locale: Locale): void {
  currentLocale = locale;
  // Clear cache when locale changes
  clearTranslationCache();
  loadMessages(locale);

  // Persist locale to localStorage
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.setItem('birdnet-locale', locale);
    } catch (error) {
      // eslint-disable-next-line no-console
      console.warn('Failed to save locale to localStorage:', error);
    }
  }
}

// Message loading with English fallback
async function loadMessages(locale: Locale): Promise<void> {
  loading = true;
  // Clear cache when loading new messages
  clearTranslationCache();
  try {
    // Use fetch to load JSON from the built assets directory
    // In production, these files are copied to dist/messages by Vite
    const response = await fetch(`/ui/assets/messages/${locale}.json`);
    if (!response.ok) {
      throw new Error(`Failed to fetch: ${response.status}`);
    }
    const data = await response.json();
    messages = data;
    // Clear cache after successfully loading new messages
    clearTranslationCache();
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error(`Failed to load messages for ${locale}:`, error);

    // Fallback to English if the requested locale fails
    if (locale !== DEFAULT_LOCALE) {
      // eslint-disable-next-line no-console
      console.info(`Falling back to ${DEFAULT_LOCALE} locale`);
      try {
        const fallbackResponse = await fetch(`/ui/assets/messages/${DEFAULT_LOCALE}.json`);
        if (fallbackResponse.ok) {
          const fallbackData = await fallbackResponse.json();
          messages = fallbackData;
          // Clear cache after loading fallback messages
          clearTranslationCache();
          return;
        }
      } catch (fallbackError) {
        // eslint-disable-next-line no-console
        console.error(`Failed to load fallback messages:`, fallbackError);
      }
    }

    // If all else fails, keep existing messages (don't clear them)
    // This prevents raw translation keys from being displayed
    // eslint-disable-next-line no-console
    console.warn('Translation loading failed, keeping existing messages to prevent UI degradation');
  } finally {
    loading = false;
  }
}

// Translation cache for memoization
const translationCache = new Map<string, { locale: string; params?: string; value: string }>();

/**
 * Helper to get nested value from object using dot notation
 * @param obj - The object to traverse
 * @param path - The dot-separated path to the value
 * @returns The value at the path or undefined
 * @internal
 */
function getNestedValue(obj: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((current, key) => {
    if (current && typeof current === 'object' && key in current) {
      // Safe property access for message keys
      // eslint-disable-next-line security/detect-object-injection
      return (current as Record<string, unknown>)[key];
    }
    return undefined;
  }, obj);
}

/**
 * Clear translation cache when locale changes
 * @internal
 */
function clearTranslationCache(): void {
  translationCache.clear();
}

/**
 * Translation function with runtime type checking and pluralization support
 * @param key - The translation key
 * @param params - Optional parameters for interpolation
 * @returns The translated string
 */
export function t(key: string, params?: Record<string, unknown>): string {
  // Check cache first
  const paramsKey = params ? JSON.stringify(params) : '';
  const cacheKey = `${key}:${paramsKey}:${currentLocale}`;

  const cached = translationCache.get(cacheKey);
  if (cached && cached.locale === currentLocale && cached.params === paramsKey) {
    return cached.value;
  }

  // If messages haven't loaded yet, return the key
  if (Object.keys(messages).length === 0) {
    return key;
  }

  // Support nested keys with dot notation
  const value = getNestedValue(messages, key);
  const message = typeof value === 'string' ? value : key;

  // Only cache if we found an actual translation (not the key itself)
  if (!params && value !== undefined) {
    // Cache simple translations
    translationCache.set(cacheKey, {
      locale: currentLocale,
      params: paramsKey,
      value: message,
    });
  }

  if (!params) {
    return message;
  }

  // Handle ICU MessageFormat plural syntax
  let result = message;

  // Pattern: {count, plural, =0 {No results} one {# result} other {# results}}
  result = result.replace(/\{(\w+),\s*plural,([^}]+)\}/g, (match, paramName, pluralPattern) => {
    // eslint-disable-next-line security/detect-object-injection
    const count = params[paramName];
    if (typeof count !== 'number') return match;

    // Parse plural rules
    const rules = pluralPattern.match(/(?:=(\d+)|zero|one|two|few|many|other)\s*\{([^}]+)\}/g);
    if (!rules) return match;

    // Get the correct plural category
    const pluralRules = new Intl.PluralRules(currentLocale);
    const category = pluralRules.select(count);

    for (const rule of rules) {
      const ruleMatch = rule.match(/(?:=(\d+)|(zero|one|two|few|many|other))\s*\{([^}]+)\}/);
      if (!ruleMatch) continue;

      const [, exactMatch, pluralCategory, text] = ruleMatch;

      // Check exact match first (e.g., =0)
      if (exactMatch && Number(exactMatch) === count) {
        return text.replace(/#/g, count.toString());
      }

      // Check plural category
      if (pluralCategory === category) {
        return text.replace(/#/g, count.toString());
      }

      // Fallback to 'other' category
      if (pluralCategory === 'other') {
        return text.replace(/#/g, count.toString());
      }
    }

    return match;
  });

  // Simple interpolation: {name} -> value
  result = result.replace(/\{(\w+)\}/g, (_, param) => {
    // Safe property access for parameters
    // eslint-disable-next-line security/detect-object-injection
    return params[param]?.toString() ?? `{${param}}`;
  });

  // Only cache if we found an actual translation (not the key itself)
  if (value !== undefined) {
    // Cache the computed result
    translationCache.set(cacheKey, {
      locale: currentLocale,
      params: paramsKey,
      value: result,
    });
  }

  return result;
}

/**
 * Type-safe translation function (for future use when all components are updated)
 * Currently, the type imports are here for future implementation
 * @internal
 */
// Note: TypedTranslateFunction type will be added when type-safe translation is implemented

// Initialize on module load
if (typeof window !== 'undefined') {
  loadMessages(getLocale());
}

/**
 * Check if translations are currently being loaded
 * @returns True if translations are loading, false otherwise
 */
export function isLoading(): boolean {
  return loading;
}

/**
 * Check if translations are loaded
 * @returns True if translations are available, false otherwise
 */
export function hasTranslations(): boolean {
  return Object.keys(messages).length > 0;
}

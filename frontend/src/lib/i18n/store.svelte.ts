/* eslint-disable no-undef */
import { DEFAULT_LOCALE, type Locale, isValidLocale } from './config.js';
import { detectBrowserLocale } from './utils.js';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('app');
// Note: Type imports will be used when type-safe translation is implemented
// import type { TranslationKey, GetParams } from './types.generated.js';

// Initialize locale from localStorage, browser preferences, or use default
function getInitialLocale(): Locale {
  if (typeof localStorage !== 'undefined') {
    const stored = localStorage.getItem('birdnet-locale');
    if (stored && isValidLocale(stored)) {
      return stored;
    }
  }

  // If no stored preference, use browser locale detection
  return detectBrowserLocale();
}

// State management with Svelte 5 runes
let currentLocale = $state<Locale>(getInitialLocale());
let messages = $state<Record<string, string>>({});
let loading = $state(false);
// Keep previous messages while loading new ones
let previousMessages = $state<Record<string, string>>({});
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
  // Don't clear cache here - keep old translations while loading new ones
  loadMessages(locale);

  // Persist locale to localStorage
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.setItem('birdnet-locale', locale);
    } catch (error) {
      logger.warn('Failed to save locale to localStorage:', error);
    }
  }
}

// Sequence counter to track the latest loadMessages request
let loadSequence = 0;

// Message loading with English fallback and race condition protection
async function loadMessages(locale: Locale): Promise<void> {
  // Increment sequence for this request
  const currentSequence = ++loadSequence;

  loading = true;
  // Store current messages as previous before loading new ones
  if (Object.keys(messages).length > 0) {
    previousMessages = { ...messages };
  }

  try {
    // Use fetch to load JSON from the built assets directory
    // In production, these files are copied to dist/messages by Vite
    const response = await fetch(`/ui/assets/messages/${locale}.json`);
    if (!response.ok) {
      throw new Error(`Failed to fetch: ${response.status}`);
    }
    const data = await response.json();

    // Check if this response is still the latest request
    if (currentSequence !== loadSequence) {
      // This request has been superseded, discard the result
      return;
    }

    messages = data;
    // Clear previous messages after successful load
    previousMessages = {};
    // Clear cache only after successfully loading new messages
    clearTranslationCache();
    // Update localStorage cache
    try {
      localStorage.setItem(`birdnet-messages-${locale}`, JSON.stringify(messages));
    } catch {
      // Ignore storage errors
    }
  } catch (error) {
    // Check if this response is still the latest request before handling errors
    if (currentSequence !== loadSequence) {
      // This request has been superseded, discard the result
      return;
    }

    logger.error(`Failed to load messages for ${locale}:`, error);

    // Fallback to English if the requested locale fails
    if (locale !== DEFAULT_LOCALE) {
      logger.info(`Falling back to ${DEFAULT_LOCALE} locale`);
      try {
        const fallbackResponse = await fetch(`/ui/assets/messages/${DEFAULT_LOCALE}.json`);
        if (fallbackResponse.ok) {
          const fallbackData = await fallbackResponse.json();

          // Check again if this is still the latest request
          if (currentSequence !== loadSequence) {
            return;
          }

          messages = fallbackData;
          // Clear previous messages after successful fallback load
          previousMessages = {};
          // Clear cache after loading fallback messages
          clearTranslationCache();
          // Update localStorage cache for fallback
          try {
            localStorage.setItem(`birdnet-messages-${DEFAULT_LOCALE}`, JSON.stringify(messages));
          } catch {
            // Ignore storage errors
          }
          return;
        }
      } catch (fallbackError) {
        logger.error(`Failed to load fallback messages:`, fallbackError);
      }
    }

    // If all else fails, restore previous messages
    // This prevents raw translation keys from being displayed
    if (Object.keys(previousMessages).length > 0) {
      messages = previousMessages;
      previousMessages = {};
    }

    logger.warn('Translation loading failed, restored previous messages to prevent UI degradation');
  } finally {
    // Only set loading to false if this is still the latest request
    if (currentSequence === loadSequence) {
      loading = false;
    }
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
  if (cached?.locale === currentLocale && cached.params === paramsKey) {
    return cached.value;
  }

  // If messages haven't loaded yet, check previousMessages first
  if (Object.keys(messages).length === 0) {
    // Check if we have the key in previousMessages
    if (Object.keys(previousMessages).length > 0) {
      const prevValue = getNestedValue(previousMessages, key);
      if (typeof prevValue === 'string') {
        // Process the message with params if needed
        if (!params) {
          return prevValue;
        }
        // Apply parameter interpolation to previous message
        let result = prevValue;
        result = result.replace(/\{(\w+)\}/g, (_, param) => {
          // eslint-disable-next-line security/detect-object-injection
          return params[param]?.toString() ?? `{${param}}`;
        });
        return result;
      }
    }

    // Try to find any cached translation for this key to prevent flickering
    for (const [cachedKey, cachedValue] of translationCache.entries()) {
      if (cachedKey.startsWith(`${key}:${paramsKey}:`)) {
        return cachedValue.value;
      }
    }
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
  // Load messages immediately and synchronously if possible
  const locale = getLocale();
  loading = true;

  // Try to load messages synchronously from cache if available
  const cachedMessages = localStorage.getItem(`birdnet-messages-${locale}`);
  if (cachedMessages) {
    try {
      messages = JSON.parse(cachedMessages);
      loading = false;
    } catch {
      // Continue with async load
    }
  }

  // Always load fresh messages asynchronously
  loadMessages(locale).then(() => {
    // Cache messages for next time
    if (Object.keys(messages).length > 0) {
      try {
        localStorage.setItem(`birdnet-messages-${locale}`, JSON.stringify(messages));
      } catch {
        // Ignore storage errors
      }
    }
  });
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

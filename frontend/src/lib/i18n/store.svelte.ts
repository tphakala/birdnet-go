/* eslint-disable no-undef */
import { DEFAULT_LOCALE, type Locale, isValidLocale } from './config.js';
import { detectBrowserLocale } from './utils.js';
import { parsePlural } from './pluralParser.js';
import { getLogger } from '$lib/utils/logger';
import { buildAppUrl } from '$lib/utils/urlHelpers';

const logger = getLogger('app');
// Note: Type imports will be used when type-safe translation is implemented
// import type { TranslationKey, GetParams } from './types.generated.js';

/**
 * Critical fallback translations for keys needed before async load completes.
 * These prevent showing raw keys during initial render before translations load.
 * Only include strings needed for the loading/error screens.
 *
 * IMPORTANT: Keep these in sync with en.json. These are intentional duplicates
 * to ensure the loading UI works before any network request completes.
 */
const CRITICAL_FALLBACKS: Record<string, string> = {
  'common.loading': 'Loading...',
  'common.error': 'Error',
  'common.retry': 'Retry',
  'common.retrying': 'Retrying',
  'error.server.title': 'Server Connection Error',
  'error.server.description':
    'Unable to connect to the server. Please check your connection and try again.',
  'error.generic.componentLoadError': 'Component Load Error',
  'error.generic.failedToLoadComponent': 'Failed to load the requested component',
};

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
// Track if initial translation load has completed (for first load only)
let initialLoadComplete = $state(false);

// Build version for cache invalidation. Changes on every build via Vite define.
// Falls back to 'dev' so dev/test mode always fetches fresh translations.
const I18N_CACHE_VERSION: string =
  typeof __I18N_CACHE_VERSION__ !== 'undefined' ? __I18N_CACHE_VERSION__ : 'dev';

const CACHE_KEY_PREFIX = 'birdnet-messages';

function cacheKey(locale: string): string {
  return `${CACHE_KEY_PREFIX}-${locale}-${I18N_CACHE_VERSION}`;
}
/* eslint-enable no-undef */

// Promise that resolves when translations are first loaded
let initialLoadResolver: (() => void) | null = null;
const initialLoadPromise = new Promise<void>(resolve => {
  initialLoadResolver = resolve;
});

/**
 * Mark initial load as complete and resolve the promise.
 * This is a helper to avoid TypeScript narrowing issues with the resolver.
 * @internal
 */
function markInitialLoadComplete(): void {
  if (!initialLoadComplete) {
    initialLoadComplete = true;
    if (initialLoadResolver) {
      initialLoadResolver();
    }
  }
}

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
    const response = await fetch(
      buildAppUrl(`/ui/assets/messages/${locale}.json?v=${I18N_CACHE_VERSION}`)
    );
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
    // Mark initial load as complete and resolve the promise
    markInitialLoadComplete();
    // Update localStorage cache
    try {
      localStorage.setItem(cacheKey(locale), JSON.stringify(messages));
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
        const fallbackResponse = await fetch(
          buildAppUrl(`/ui/assets/messages/${DEFAULT_LOCALE}.json?v=${I18N_CACHE_VERSION}`)
        );
        if (fallbackResponse.ok) {
          const fallbackData = await fallbackResponse.json();

          // Check again if this is still the latest request
          if (currentSequence !== loadSequence) {
            return;
          }

          messages = fallbackData;
          // Clear previous messages after successful fallback load
          previousMessages = {};
          // Mark initial load as complete and resolve the promise
          markInitialLoadComplete();
          // Update localStorage cache for fallback
          try {
            localStorage.setItem(cacheKey(DEFAULT_LOCALE), JSON.stringify(messages));
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

    // Mark initial load as complete even on failure to prevent waitForTranslations() from hanging
    // Components will use critical fallbacks for essential UI strings
    markInitialLoadComplete();

    logger.warn('Translation loading failed, restored previous messages to prevent UI degradation');
  } finally {
    // Only set loading to false if this is still the latest request
    if (currentSequence === loadSequence) {
      loading = false;
    }
  }
}

// Remove old localStorage caches from previous versions.
function cleanupOldCaches(): void {
  try {
    const keysToRemove: string[] = [];
    const currentVersionSuffix = `-${I18N_CACHE_VERSION}`;
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key?.startsWith(CACHE_KEY_PREFIX) && !key.endsWith(currentVersionSuffix)) {
        keysToRemove.push(key);
      }
    }
    for (const key of keysToRemove) {
      localStorage.removeItem(key);
    }
  } catch {
    // Ignore errors during cleanup
  }
}

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
 * Translation function with runtime type checking and pluralization support
 * @param key - The translation key
 * @param params - Optional parameters for interpolation
 * @returns The translated string
 */
export function t(key: string, params?: Record<string, unknown>): string {
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

    // Check critical fallbacks for essential UI strings (loading/error screens)
    // This prevents showing raw keys during initial render before translations load
    if (key in CRITICAL_FALLBACKS) {
      // eslint-disable-next-line security/detect-object-injection
      return CRITICAL_FALLBACKS[key];
    }

    return key;
  }

  // Support nested keys with dot notation
  const value = getNestedValue(messages, key);
  const message = typeof value === 'string' ? value : key;

  if (!params) {
    return message;
  }

  // Handle ICU MessageFormat plural syntax using extracted parser
  let result = parsePlural(message, params, currentLocale);

  // Simple interpolation: {name} -> value
  result = result.replace(/\{(\w+)\}/g, (_, param) => {
    // Safe property access for parameters
    // eslint-disable-next-line security/detect-object-injection
    return params[param]?.toString() ?? `{${param}}`;
  });

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
  cleanupOldCaches();
  loading = true;

  // Try to load messages synchronously from cache if available
  const cachedMessages = localStorage.getItem(cacheKey(locale));
  if (cachedMessages) {
    try {
      messages = JSON.parse(cachedMessages);
      loading = false;
      // Mark initial load as complete since we have cached translations
      markInitialLoadComplete();
    } catch {
      // Continue with async load
    }
  }

  // Always load fresh messages asynchronously
  loadMessages(locale).then(() => {
    // Cache messages for next time
    if (Object.keys(messages).length > 0) {
      try {
        localStorage.setItem(cacheKey(locale), JSON.stringify(messages));
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

/**
 * Check if translations are ready for use (initial load completed)
 * This is reactive and can be used in $derived expressions.
 * @returns True if initial translation load has completed
 */
export function translationsReady(): boolean {
  return initialLoadComplete;
}

/**
 * Wait for translations to be loaded.
 * Returns a promise that resolves when translations are available.
 * Useful for components that need to wait before rendering translated content.
 * @returns Promise that resolves when translations are loaded
 */
export function waitForTranslations(): Promise<void> {
  // If already loaded, resolve immediately
  if (initialLoadComplete) {
    return Promise.resolve();
  }
  return initialLoadPromise;
}

/**
 * Get the critical fallback translations for testing.
 * @internal
 */
export function getCriticalFallbacks(): Record<string, string> {
  return { ...CRITICAL_FALLBACKS };
}

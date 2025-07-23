/* eslint-disable no-undef */
import { DEFAULT_LOCALE, type Locale, isValidLocale } from './config.js';
import type { TranslationKey, GetParams } from './types.generated.js';

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
  loadMessages(locale);
}

// Message loading with English fallback
async function loadMessages(locale: Locale): Promise<void> {
  loading = true;
  try {
    // Use fetch to load JSON from the built assets directory
    // In production, these files are copied to dist/messages by Vite
    const response = await fetch(`/ui/assets/messages/${locale}.json`);
    if (!response.ok) {
      throw new Error(`Failed to fetch: ${response.status}`);
    }
    const data = await response.json();
    messages = data;
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
          return;
        }
      } catch (fallbackError) {
        // eslint-disable-next-line no-console
        console.error(`Failed to load fallback messages:`, fallbackError);
      }
    }
    
    // If all else fails, set empty messages
    messages = {};
  } finally {
    loading = false;
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
 * Translation function with runtime type checking
 * @param key - The translation key
 * @param params - Optional parameters for interpolation
 * @returns The translated string
 */
export function t(key: string, params?: Record<string, unknown>): string {
  // Support nested keys with dot notation
  const value = getNestedValue(messages, key);
  const message = typeof value === 'string' ? value : key;

  if (!params) return message;

  // Simple interpolation: {name} -> value
  return message.replace(/\{(\w+)\}/g, (_, param) => {
    // Safe property access for parameters
    // eslint-disable-next-line security/detect-object-injection
    return params[param]?.toString() ?? `{${param}}`;
  });
}

/**
 * Type-safe translation function (for future use when all components are updated)
 * Currently, the type imports are here for future implementation
 * @internal
 */
// eslint-disable-next-line @typescript-eslint/no-unused-vars
type TypedTranslateFunction = <K extends TranslationKey>(
  key: K,
  ...args: GetParams<K> extends never ? [] : [GetParams<K>]
) => string;

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

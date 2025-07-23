/* eslint-disable no-undef */
import { DEFAULT_LOCALE, type Locale, isValidLocale } from './config.js';

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

// Locale getter/setter
export function getLocale(): Locale {
  return currentLocale;
}

export function setLocale(locale: Locale): void {
  currentLocale = locale;
  loadMessages(locale);
}

// Message loading
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
    messages = {};
  } finally {
    loading = false;
  }
}

// Helper to get nested value from object using dot notation
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

// Translation function
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

// Initialize on module load
if (typeof window !== 'undefined') {
  loadMessages(getLocale());
}

// Export loading state for UI
export function isLoading(): boolean {
  return loading;
}

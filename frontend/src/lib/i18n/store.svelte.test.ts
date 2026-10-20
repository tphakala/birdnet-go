/**
 * Tests for i18n store race condition handling
 * Issue: Translation keys shown instead of translated text on first load
 *
 * The race condition occurs when:
 * 1. App starts and imports i18n module
 * 2. i18n starts async fetch of translations
 * 3. Components render and call t() before fetch completes
 * 4. t() returns raw keys instead of translations
 *
 * Fix: Critical fallbacks provide essential translations immediately
 */

import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { readFileSync } from 'fs';
import { dirname, resolve } from 'path';
import { fileURLToPath } from 'url';
import {
  t,
  getCriticalFallbacks,
  waitForTranslations,
  translationsReady,
  hasTranslations,
  isLoading,
  setLocale,
} from './store.svelte';

// ESM equivalent of __dirname
const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

describe('i18n store - race condition handling', () => {
  describe('critical fallbacks', () => {
    it('should have critical fallbacks defined', () => {
      const fallbacks = getCriticalFallbacks();
      expect(fallbacks).toBeDefined();
      expect(Object.keys(fallbacks).length).toBeGreaterThan(0);
    });

    it('should include essential loading screen keys', () => {
      const fallbacks = getCriticalFallbacks();

      // These keys are used by App.svelte loading screen
      expect(fallbacks['common.loading']).toBe('Loading...');
      expect(fallbacks['common.error']).toBe('Error');
      expect(fallbacks['common.retry']).toBe('Retry');
      expect(fallbacks['common.retrying']).toBe('Retrying');
    });

    it('should include essential error screen keys', () => {
      const fallbacks = getCriticalFallbacks();

      expect(fallbacks['error.server.title']).toBeDefined();
      expect(fallbacks['error.server.description']).toBeDefined();
    });

    it('should return a copy, not the original object', () => {
      const fallbacks1 = getCriticalFallbacks();
      const fallbacks2 = getCriticalFallbacks();

      fallbacks1['common.loading'] = 'modified';

      // Original should not be modified
      expect(fallbacks2['common.loading']).toBe('Loading...');
    });

    it('should match values in en.json to prevent drift', () => {
      // Read the actual en.json file to verify CRITICAL_FALLBACKS stays in sync
      const enJsonPath = resolve(__dirname, '../../../static/messages/en.json');
      const enJson = JSON.parse(readFileSync(enJsonPath, 'utf8')) as Record<string, unknown>;

      // Helper to get nested value from en.json using dot notation
      const getNestedValue = (obj: Record<string, unknown>, path: string): unknown => {
        return path.split('.').reduce<unknown>((current, key) => {
          if (current && typeof current === 'object' && key in current) {
            // eslint-disable-next-line security/detect-object-injection -- key is from controlled path.split()
            return (current as Record<string, unknown>)[key];
          }
          return undefined;
        }, obj);
      };

      const fallbacks = getCriticalFallbacks();

      // Verify each critical fallback matches the corresponding en.json value
      for (const [key, fallbackValue] of Object.entries(fallbacks)) {
        const enJsonValue = getNestedValue(enJson, key);
        expect(enJsonValue, `CRITICAL_FALLBACKS['${key}'] should match en.json`).toBe(
          fallbackValue
        );
      }
    });
  });

  describe('t() function with critical fallbacks', () => {
    // Note: These tests rely on the test setup.ts mocking fetch
    // The mock fetch returns translations, so messages will be loaded

    it('should return translation when messages are loaded', async () => {
      // Wait for translations to load from mock fetch
      await waitForTranslations();

      // The mock translations in setup.ts include 'common.loading'
      const result = t('common.loading');
      expect(result).toBe('Loading...');
    });

    it('should handle nested keys', async () => {
      await waitForTranslations();

      // Test nested key from mock translations
      const result = t('common.buttons.save');
      expect(result).toBe('Save Changes');
    });

    it('should return key for unknown translations', async () => {
      await waitForTranslations();

      const result = t('unknown.key.that.does.not.exist');
      expect(result).toBe('unknown.key.that.does.not.exist');
    });
  });

  describe('waitForTranslations()', () => {
    it('should resolve when translations are loaded', async () => {
      // This should not timeout - translations should load from mock
      await expect(waitForTranslations()).resolves.toBeUndefined();
    });

    it('should resolve immediately if already loaded', async () => {
      // First wait
      await waitForTranslations();

      // Second wait should resolve immediately
      const start = Date.now();
      await waitForTranslations();
      const elapsed = Date.now() - start;

      expect(elapsed).toBeLessThan(10); // Should be nearly instant
    });
  });

  describe('translationsReady()', () => {
    it('should return true after translations load', async () => {
      await waitForTranslations();
      expect(translationsReady()).toBe(true);
    });
  });

  describe('hasTranslations()', () => {
    it('should return true when translations are loaded', async () => {
      await waitForTranslations();
      expect(hasTranslations()).toBe(true);
    });
  });

  describe('isLoading()', () => {
    it('should return false after load completes', async () => {
      await waitForTranslations();
      expect(isLoading()).toBe(false);
    });
  });
});

describe('i18n store - locale switching', () => {
  it('should handle setLocale without crashing', async () => {
    await waitForTranslations();

    // This should not throw
    expect(() => setLocale('en')).not.toThrow();
  });
});

describe('i18n store - localStorage cache', () => {
  // Clean up any stale keys injected by tests to avoid leaking state.
  afterEach(() => {
    localStorage.removeItem('birdnet-messages-en-oldversion');
  });

  // Note: This test runs after the i18n module has already initialized, so it
  // doesn't exercise the startup cache-read path. It verifies that stale entries
  // from old versions don't interfere with the already-loaded fresh translations.
  it('should not use stale localStorage cache after version change', async () => {
    // Simulate stale cache from old version
    localStorage.setItem(
      'birdnet-messages-en-oldversion',
      JSON.stringify({
        common: { loading: 'Stale Loading...' },
      })
    );

    // Wait for fresh translations
    await waitForTranslations();

    // Should use fresh translations, not stale cache
    const result = t('common.loading');
    expect(result).toBe('Loading...');
  });
});

describe('i18n store - blocked storage', () => {
  // When site data is blocked, merely reading window.localStorage throws a
  // SecurityError. The store reads it at module init, so a fresh import must
  // survive that and fall back to a valid locale.
  const originalDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage');

  afterEach(() => {
    if (originalDescriptor) {
      Object.defineProperty(window, 'localStorage', originalDescriptor);
    }
    vi.resetModules();
  });

  it('should initialize and switch locale without throwing when storage access throws', async () => {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() {
        throw new DOMException('The operation is insecure.', 'SecurityError');
      },
    });
    vi.resetModules();

    const store = await import('./store.svelte');
    const { isValidLocale } = await import('./config');

    expect(isValidLocale(store.getLocale())).toBe(true);
    expect(() => store.setLocale('fi')).not.toThrow();
    expect(store.getLocale()).toBe('fi');
  });
});

describe('i18n store - locale loading', () => {
  const originalFetch = vi.mocked(globalThis.fetch).getMockImplementation();

  beforeEach(() => {
    // Earlier tests in this file leave cached messages behind
    localStorage.clear();
  });

  afterEach(() => {
    vi.mocked(globalThis.fetch).mockReset();
    if (originalFetch) vi.mocked(globalThis.fetch).mockImplementation(originalFetch);
    localStorage.removeItem('birdnet-locale');
    document.documentElement.lang = 'en';
    vi.resetModules();
  });

  /**
   * Makes requests for the given locales' message files fail and, when english
   * is given, serves it as the English message file; everything else is served
   * as usual.
   */
  function mockMessageFetch(failing: string[], english?: Record<string, unknown>) {
    vi.mocked(globalThis.fetch).mockImplementation(
      (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (failing.some(locale => url.includes(`/ui/assets/messages/${locale}.json`))) {
          return Promise.resolve(new Response('not found', { status: 404 }));
        }
        if (english && url.includes('/ui/assets/messages/en.json')) {
          return Promise.resolve(Response.json(english));
        }
        if (!originalFetch) throw new Error('fetch mock has no implementation');
        return originalFetch(input, init);
      }
    );
  }

  /**
   * Holds every message file request until the test settles it, so loads can
   * finish in any order; other requests are served as usual. Responses settle
   * through microtasks only, so one flushTasks() call lets a load act on them.
   */
  function holdMessageFetch() {
    const requested: string[] = [];
    const pending: { locale: string; resolve: (response: Response) => void }[] = [];
    vi.mocked(globalThis.fetch).mockImplementation(
      (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const match = /\/ui\/assets\/messages\/(\w+)\.json/.exec(url);
        if (match) {
          const locale = match[1];
          requested.push(locale);
          return new Promise<Response>(resolve => pending.push({ locale, resolve }));
        }
        if (!originalFetch) throw new Error('fetch mock has no implementation');
        return originalFetch(input, init);
      }
    );
    return {
      /** Number of requests made for the locale's message file */
      requests: (locale: string) => requested.filter(item => item === locale).length,
      /** Settles the oldest open request for the locale; null makes it fail */
      settle(locale: string, body: Record<string, string> | null) {
        const index = pending.findIndex(item => item.locale === locale);
        if (index === -1) throw new Error(`no open request for ${locale}`);
        const [request] = pending.splice(index, 1);
        // json() settles through microtasks only, unlike a streamed body
        request.resolve(
          Object.assign(new Response(null, { status: body === null ? 404 : 200 }), {
            json: () => Promise.resolve(body),
          })
        );
      },
    };
  }

  /** Runs every queued microtask, so a settled load finishes acting on its response. */
  const flushTasks = () => new Promise(resolve => setTimeout(resolve, 0));

  /** Messages each locale serves in the held-fetch tests. */
  const greetings: Record<string, Record<string, string>> = {
    en: { greeting: 'Hello' },
    de: { greeting: 'Hallo' },
    fi: { greeting: 'Hei' },
  };

  /** Starts the store in English with the held fetch, so loads can be ordered. */
  async function startHeld() {
    const fetches = holdMessageFetch();
    localStorage.setItem('birdnet-locale', 'en');
    vi.resetModules();
    const store = await import('./store.svelte');
    fetches.settle('en', greetings.en);
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    return { store, fetches };
  }

  /** Message cache entries in localStorage for the locale. */
  const cacheKeysFor = (locale: string) =>
    Object.keys(localStorage).filter(key => key.startsWith(`birdnet-messages-${locale}-`));

  /**
   * The store's cache key for a locale, found by letting it load that locale
   * once with the default fetch mock; it leaves birdnet-locale set to locale.
   */
  async function messageCacheKey(locale: string): Promise<string> {
    localStorage.setItem('birdnet-locale', locale);
    vi.resetModules();
    const probe = await import('./store.svelte');
    await vi.waitFor(() => expect(cacheKeysFor(locale)).toHaveLength(1));
    await vi.waitFor(() => expect(probe.isLoading()).toBe(false));
    return cacheKeysFor(locale)[0];
  }

  /**
   * Warnings the store logged for a total translation load failure. The
   * logger is mocked in test setup; the store's instance is the last one
   * created for the 'app' category, so call this after importing the store.
   */
  async function loadFailureWarnings(): Promise<unknown[][]> {
    const { getLogger } = await import('$lib/utils/logger');
    const getLoggerMock = vi.mocked(getLogger);
    const index = getLoggerMock.mock.calls.map(call => call[0]).lastIndexOf('app');
    const result = getLoggerMock.mock.results.at(index);
    if (index === -1 || result?.type !== 'return') throw new Error('store logger not found');
    return vi
      .mocked(result.value.warn)
      .mock.calls.filter(call =>
        call.some(arg => String(arg).includes('Translation loading failed'))
      );
  }

  it('sets the html lang attribute once the new locale messages have loaded', async () => {
    const store = await import('./store.svelte');
    await store.waitForTranslations();

    store.setLocale('de');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    expect(document.documentElement.lang).toBe('de');

    store.setLocale('fi');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    expect(document.documentElement.lang).toBe('fi');
  });

  it('marks the page English when the locale fails to load and English is shown instead', async () => {
    const store = await import('./store.svelte');
    await store.waitForTranslations();
    store.setLocale('fi');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    mockMessageFetch(['de']);

    store.setLocale('de');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));

    expect(document.documentElement.lang).toBe('en');
  });

  it('keeps the previous language when both the locale and the English fallback fail', async () => {
    const store = await import('./store.svelte');
    await store.waitForTranslations();
    store.setLocale('fi');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    mockMessageFetch(['de', 'en']);

    store.setLocale('de');
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));

    expect(document.documentElement.lang).toBe('fi');
  });

  it('sets the html lang attribute from the initial locale on load', async () => {
    localStorage.setItem('birdnet-locale', 'sv');
    vi.resetModules();

    const store = await import('./store.svelte');

    expect(store.getLocale()).toBe('sv');
    expect(document.documentElement.lang).toBe('sv');
  });

  it('does not cache the English fallback under the locale that failed to load at startup', async () => {
    mockMessageFetch(['fi']);
    localStorage.setItem('birdnet-locale', 'fi');

    const store = await import('./store.svelte');
    await store.waitForTranslations();
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));

    expect(store.t('common.loading')).toBe('Loading...');
    const cacheKeys = Object.keys(localStorage).filter(key => key.startsWith('birdnet-messages-'));
    expect(cacheKeys).toHaveLength(1);
    expect(cacheKeys[0]).toMatch(/^birdnet-messages-en-/);
  });

  it('applies the plural rules of the English fallback when the selected locale fails to load', async () => {
    // French treats 0 as singular, English does not
    mockMessageFetch(['fr'], {
      detections: '{count, plural, one {# detection} other {# detections}}',
    });
    localStorage.setItem('birdnet-locale', 'fr');

    const store = await import('./store.svelte');
    await store.waitForTranslations();
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));

    expect(store.getLocale()).toBe('fr');
    expect(store.t('detections', { count: 0 })).toBe('0 detections');
    expect(store.t('detections', { count: 1 })).toBe('1 detection');
  });

  it('ignores a superseded switch whose messages arrive after a newer switch finished', async () => {
    const { store, fetches } = await startHeld();

    store.setLocale('de');
    store.setLocale('fi');
    fetches.settle('fi', greetings.fi);
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    fetches.settle('de', greetings.de);
    await flushTasks();

    expect(store.t('greeting')).toBe('Hei');
    expect(document.documentElement.lang).toBe('fi');
    expect(cacheKeysFor('de')).toEqual([]);
  });

  it('does not fall back or report a failure for a superseded switch whose locale fails late', async () => {
    const { store, fetches } = await startHeld();

    store.setLocale('de');
    store.setLocale('fi');
    fetches.settle('fi', greetings.fi);
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    fetches.settle('de', null);
    await flushTasks();

    expect(fetches.requests('en')).toBe(1);
    expect(await loadFailureWarnings()).toEqual([]);
    expect(store.t('greeting')).toBe('Hei');
  });

  it.each([
    { outcome: 'fails', english: null },
    { outcome: 'succeeds', english: greetings.en },
  ])(
    'ignores the English fallback of a superseded switch when it $outcome after a newer switch finished',
    async ({ english }) => {
      const { store, fetches } = await startHeld();

      store.setLocale('de');
      fetches.settle('de', null);
      await flushTasks();
      expect(fetches.requests('en')).toBe(2);
      store.setLocale('fi');
      fetches.settle('fi', greetings.fi);
      await vi.waitFor(() => expect(store.isLoading()).toBe(false));
      fetches.settle('en', english);
      await flushTasks();

      expect(store.t('greeting')).toBe('Hei');
      expect(document.documentElement.lang).toBe('fi');
      expect(await loadFailureWarnings()).toEqual([]);
    }
  );

  it('keeps loading until the newest switch settles when a superseded one settles first', async () => {
    const { store, fetches } = await startHeld();

    store.setLocale('de');
    store.setLocale('fi');
    fetches.settle('de', greetings.de);
    await flushTasks();

    expect(store.isLoading()).toBe(true);
    expect(store.t('greeting')).toBe('Hello');

    fetches.settle('fi', greetings.fi);
    await vi.waitFor(() => expect(store.isLoading()).toBe(false));
    expect(store.t('greeting')).toBe('Hei');
  });

  it('shows cached messages for the initial locale before the network load and leaves the cache entry as stored', async () => {
    const key = await messageCacheKey('fi');
    const fetches = holdMessageFetch();
    // Not in JSON.stringify's form, so a rewrite of the entry would show
    const stored = '{ "greeting" : "Hei välimuistista" }';
    localStorage.setItem(key, stored);
    vi.resetModules();

    const store = await import('./store.svelte');

    expect(store.translationsReady()).toBe(true);
    expect(store.t('greeting')).toBe('Hei välimuistista');
    expect(document.documentElement.lang).toBe('fi');
    expect(localStorage.getItem(key)).toBe(stored);
    expect(fetches.requests('fi')).toBe(1);
  });

  it('waits for the network load when the cached messages are not valid JSON', async () => {
    const key = await messageCacheKey('fi');
    const fetches = holdMessageFetch();
    localStorage.setItem(key, '{');
    vi.resetModules();

    const store = await import('./store.svelte');
    expect(store.translationsReady()).toBe(false);

    fetches.settle('fi', greetings.fi);
    await vi.waitFor(() => expect(store.translationsReady()).toBe(true));
    expect(store.t('greeting')).toBe('Hei');
  });

  it('shows the loaded locale when writing the message cache throws', async () => {
    localStorage.setItem('birdnet-locale', 'fi');
    const originalSetItem = Storage.prototype.setItem;
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (
      this: Storage,
      key: string,
      value: string
    ) {
      if (key.startsWith('birdnet-messages-')) {
        throw new DOMException('The quota has been exceeded.', 'QuotaExceededError');
      }
      originalSetItem.call(this, key, value);
    });
    try {
      vi.resetModules();
      const store = await import('./store.svelte');
      await store.waitForTranslations();
      await vi.waitFor(() => expect(store.isLoading()).toBe(false));

      expect(document.documentElement.lang).toBe('fi');
      expect(cacheKeysFor('en')).toEqual([]);
    } finally {
      setItem.mockRestore();
    }
  });
});

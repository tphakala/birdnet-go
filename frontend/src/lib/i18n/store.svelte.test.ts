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

import { describe, it, expect, afterEach } from 'vitest';
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

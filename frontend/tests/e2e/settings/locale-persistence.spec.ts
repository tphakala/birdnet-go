import { test, expect, type Page } from '@playwright/test';

/**
 * Locale Persistence E2E Tests
 *
 * Validates that the language selector correctly:
 * - Sets the locale via the LanguageSelector dropdown
 * - Persists locale in localStorage as 'birdnet-locale'
 * - Restores the correct locale after page reload
 * - Displays translated text for every supported locale
 *
 * This test suite covers all 10 supported locales and specifically
 * catches the bug where certain locales (e.g., Slovak) revert to
 * English after reload due to backend validation resetting the value.
 */

// All supported locales with their display names and a known translated string.
// We use "common.settings" (the translation for "Settings" in the navigation)
// because it differs across every locale and is always visible in the sidebar.
const LOCALE_DATA: {
  code: string;
  name: string;
  settingsTranslation: string;
}[] = [
  { code: 'en', name: 'English', settingsTranslation: 'Settings' },
  { code: 'de', name: 'Deutsch', settingsTranslation: 'Einstellungen' },
  { code: 'es', name: 'Espanol', settingsTranslation: 'Configuración' },
  { code: 'fi', name: 'Suomi', settingsTranslation: 'Asetukset' },
  { code: 'fr', name: 'Francais', settingsTranslation: 'Paramètres' },
  { code: 'it', name: 'Italiano', settingsTranslation: 'Impostazioni' },
  { code: 'nl', name: 'Nederlands', settingsTranslation: 'Instellingen' },
  { code: 'pl', name: 'Polski', settingsTranslation: 'Ustawienia' },
  { code: 'pt', name: 'Portugues', settingsTranslation: 'Configurações' },
  { code: 'sk', name: 'Slovenčina', settingsTranslation: 'Nastavenia' },
];

/** Clear the locale from localStorage to start with a clean state. */
const clearLocaleStorage = async (page: Page) => {
  await page.evaluate(() => {
    localStorage.removeItem('birdnet-locale');
    // Also clear cached message bundles that might interfere
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (key?.startsWith('birdnet-messages')) {
        localStorage.removeItem(key);
      }
    }
  });
};

/** Get the stored locale value from localStorage. */
const getStoredLocale = (page: Page): Promise<string | null> =>
  page.evaluate(() => localStorage.getItem('birdnet-locale'));

/**
 * Select a locale using the LanguageSelector dropdown.
 *
 * The LanguageSelector uses SelectDropdown with variant="select".
 * Interaction: click the trigger button to open the listbox,
 * then click the option with the matching locale name.
 */
const selectLocale = async (page: Page, localeName: string) => {
  // The LanguageSelector renders a SelectDropdown. The trigger is a <button>
  // with aria-haspopup="listbox". We find it by looking for the button that
  // currently shows a locale name (it renders the selected locale's name).
  // There may be multiple buttons with aria-haspopup; we find the one inside
  // a container that shows flag icons and locale names.

  // Find the language selector button - it contains the flag and language name
  // and has aria-haspopup="listbox"
  const languageSelectorButton = page.locator('button[aria-haspopup="listbox"]').filter({
    has: page.locator('img[alt*="flag"], svg, span'),
  });

  // If there are multiple matching buttons, use the first visible one
  // that contains a known locale name
  const allButtons = page.locator('button[aria-haspopup="listbox"]');
  const buttonCount = await allButtons.count();

  let selectorButton = languageSelectorButton.first();

  // Fallback: iterate to find the button that contains a known locale name
  if (buttonCount > 1) {
    for (let i = 0; i < buttonCount; i++) {
      const btn = allButtons.nth(i);
      const text = await btn.textContent();
      const isLocaleButton = LOCALE_DATA.some(l => text?.includes(l.name));
      if (isLocaleButton) {
        selectorButton = btn;
        break;
      }
    }
  }

  await expect(selectorButton).toBeVisible();
  await selectorButton.click();

  // Wait for the dropdown listbox to appear
  const listbox = page.locator('[role="listbox"]');
  await expect(listbox).toBeVisible();

  // Click the option with the target locale name
  const option = listbox.locator('[role="option"]').filter({ hasText: localeName });
  await expect(option).toBeVisible();
  await option.click();

  // Wait for the dropdown to close (single-select mode closes on selection)
  await expect(listbox).toBeHidden();
};

/**
 * Wait for translations to load after a locale change or page load.
 * We poll for the expected translated text to appear, giving the async
 * translation fetch time to complete.
 */
const waitForTranslation = async (page: Page, expectedText: string, timeout = 10000) => {
  await expect(page.getByText(expectedText, { exact: false }).first()).toBeVisible({ timeout });
};

test.describe('Locale Persistence', () => {
  test.setTimeout(60000);

  test.beforeEach(async ({ page }) => {
    // Navigate to a page first to access localStorage
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });
    await clearLocaleStorage(page);
  });

  for (const locale of LOCALE_DATA) {
    test(`locale "${locale.code}" (${locale.name}) persists after page reload`, async ({
      page,
    }) => {
      // Navigate to settings page where the LanguageSelector is accessible
      await page.goto('/ui/settings', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Select the target locale via the dropdown
      await selectLocale(page, locale.name);

      // Verify localStorage was set correctly
      const storedLocale = await getStoredLocale(page);
      expect(storedLocale, `localStorage should contain "${locale.code}"`).toBe(locale.code);

      // Wait for translations to load and verify translated text appears
      await waitForTranslation(page, locale.settingsTranslation);

      // Reload the page
      await page.reload({ waitUntil: 'domcontentloaded' });

      // Verify localStorage still has the correct locale after reload
      const storedLocaleAfterReload = await getStoredLocale(page);
      expect(
        storedLocaleAfterReload,
        `localStorage should still contain "${locale.code}" after reload`
      ).toBe(locale.code);

      // Verify the translated text is still displayed after reload
      // This catches the bug where the locale reverts to 'en' on reload
      await waitForTranslation(page, locale.settingsTranslation);

      // Double-check: the page should NOT show the English text (unless locale IS English)
      if (locale.code !== 'en' && locale.settingsTranslation !== 'Settings') {
        // Wait for translations to fully load, then verify we don't have English fallback
        // showing as the primary navigation text. We check that the locale-specific
        // translation exists rather than checking absence of English, since some
        // UI elements may always show English text.
        const translatedElements = page.getByText(locale.settingsTranslation, { exact: false });
        const count = await translatedElements.count();
        expect(
          count,
          `Expected translated text "${locale.settingsTranslation}" to appear`
        ).toBeGreaterThan(0);
      }
    });
  }
});

test.describe('Locale Default Behavior', () => {
  test.setTimeout(30000);

  test('fresh page load with no localStorage defaults to English or browser locale', async ({
    page,
  }) => {
    // Navigate and clear any stored locale
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });
    await clearLocaleStorage(page);

    // Reload to trigger fresh initialization
    await page.reload({ waitUntil: 'domcontentloaded' });

    // The locale should default to browser detection or English
    // We just verify that some valid locale is set and translations load
    const storedLocale = await getStoredLocale(page);

    // On a fresh load without localStorage, the app uses browser detection.
    // The stored value may be null (not yet persisted) or a valid locale.
    if (storedLocale !== null) {
      const validLocales = LOCALE_DATA.map(l => l.code);
      expect(
        validLocales,
        `Stored locale "${storedLocale}" should be a valid locale code`
      ).toContain(storedLocale);
    }

    // Verify that some translations loaded (page is not showing raw keys)
    // Check for a common element that should always be translated
    const hasTranslatedContent = await page.evaluate(() => {
      // Check that the page body doesn't contain common raw translation keys
      const bodyText = document.body.textContent || '';
      // If we see "common.settings" as literal text, translations failed
      return !bodyText.includes('common.settings') && !bodyText.includes('navigation.dashboard');
    });
    expect(hasTranslatedContent, 'Page should show translated text, not raw keys').toBe(true);
  });

  test('invalid locale in localStorage falls back gracefully', async ({ page }) => {
    // Navigate to set up localStorage access
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

    // Set an invalid locale value
    await page.evaluate(() => {
      localStorage.setItem('birdnet-locale', 'xx-invalid');
    });

    // Reload the page
    await page.reload({ waitUntil: 'domcontentloaded' });

    // The app should fall back to browser detection or English
    // It should NOT crash or show raw translation keys
    const hasTranslatedContent = await page.evaluate(() => {
      const bodyText = document.body.textContent || '';
      return !bodyText.includes('common.settings') && !bodyText.includes('navigation.dashboard');
    });
    expect(
      hasTranslatedContent,
      'Page should show translated text after invalid locale fallback'
    ).toBe(true);

    // The stored locale should either be corrected or remain invalid
    // (the app's getInitialLocale skips invalid values)
    const storedLocale = await getStoredLocale(page);
    if (storedLocale !== null && storedLocale !== 'xx-invalid') {
      const validLocales = LOCALE_DATA.map(l => l.code);
      expect(validLocales).toContain(storedLocale);
    }
  });
});

test.describe('Locale Persistence via localStorage Direct Set', () => {
  test.setTimeout(30000);

  // This test verifies the reload path specifically: set locale in localStorage
  // without using the UI, then reload and confirm it's picked up.
  // This isolates the persistence/restoration logic from the selector UI.

  for (const locale of LOCALE_DATA) {
    test(`locale "${locale.code}" set in localStorage is restored on page load`, async ({
      page,
    }) => {
      // Navigate to establish localStorage context
      await page.goto('/ui/dashboard', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Set the locale directly in localStorage
      await page.evaluate((localeCode: string) => {
        localStorage.setItem('birdnet-locale', localeCode);
        // Clear message caches so fresh translations are fetched
        for (let i = localStorage.length - 1; i >= 0; i--) {
          const key = localStorage.key(i);
          if (key?.startsWith('birdnet-messages')) {
            localStorage.removeItem(key);
          }
        }
      }, locale.code);

      // Navigate to settings page (fresh navigation reads from localStorage)
      await page.goto('/ui/settings', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Verify the locale was picked up from localStorage
      const storedLocale = await getStoredLocale(page);
      expect(storedLocale).toBe(locale.code);

      // Verify translated text appears
      await waitForTranslation(page, locale.settingsTranslation);
    });
  }
});

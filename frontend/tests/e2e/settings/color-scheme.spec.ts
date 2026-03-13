import { test, expect, type Page } from '@playwright/test';

/**
 * Color Scheme E2E Tests
 *
 * Validates that the color scheme picker correctly:
 * - Changes CSS custom properties when switching schemes
 * - Persists selection across page reloads via localStorage
 * - Works independently with both light and dark themes
 * - Supports custom color scheme with user-defined colors
 */

/** Navigate to settings and wait for the Interface section to load. */
const navigateToSettings = async (page: Page) => {
  await page.goto('/ui/settings', { timeout: 15000 });
  await page.waitForLoadState('domcontentloaded', { timeout: 10000 });
};

/** Get the computed value of a CSS custom property on the document root. */
const getCSSVariable = (page: Page, varName: string): Promise<string> =>
  page.evaluate(name => {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }, varName);

/** Get the data-scheme attribute from the html element. */
const getDataScheme = (page: Page): Promise<string | null> =>
  page.evaluate(() => document.documentElement.getAttribute('data-scheme'));

/** Click a color scheme swatch button by its aria-label text. */
const selectScheme = async (page: Page, labelText: string) => {
  const swatch = page.locator(`button[role="radio"][aria-label="${labelText}"]`);
  await expect(swatch).toBeVisible();
  await swatch.click();
};

/** Clear localStorage color scheme keys to start with clean state. */
const clearSchemeStorage = async (page: Page) => {
  await page.evaluate(() => {
    localStorage.removeItem('color-scheme');
    localStorage.removeItem('custom-scheme-colors');
  });
};

test.describe('Color Scheme Switching', () => {
  test.setTimeout(30000);

  test.beforeEach(async ({ page }) => {
    // Navigate to any page first to access localStorage
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await clearSchemeStorage(page);
  });

  test('default scheme is blue', async ({ page }) => {
    await navigateToSettings(page);

    const scheme = await getDataScheme(page);
    expect(scheme).toBe('blue');

    const primaryColor = await getCSSVariable(page, '--color-primary');
    expect(primaryColor).toBeTruthy();
  });

  test('switching to forest scheme changes CSS variables', async ({ page }) => {
    await navigateToSettings(page);

    await selectScheme(page, 'settings.appearance.schemeForest');

    // Verify data attribute changed
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('forest');

    // Verify CSS variable changed to forest green
    const primaryColor = await getCSSVariable(page, '--color-primary');
    expect(primaryColor).toBe('#047857');
  });

  test('all predefined schemes change the primary color', async ({ page }) => {
    await navigateToSettings(page);

    const schemes = [
      { label: 'settings.appearance.schemeForest', id: 'forest', color: '#047857' },
      { label: 'settings.appearance.schemeAmber', id: 'amber', color: '#d97706' },
      { label: 'settings.appearance.schemeViolet', id: 'violet', color: '#7c3aed' },
      { label: 'settings.appearance.schemeRose', id: 'rose', color: '#e11d48' },
      { label: 'settings.appearance.schemeBlue', id: 'blue', color: '#2563eb' },
    ];

    for (const { label, id, color } of schemes) {
      await selectScheme(page, label);

      const scheme = await getDataScheme(page);
      expect(scheme, `Expected data-scheme to be "${id}"`).toBe(id);

      const primaryColor = await getCSSVariable(page, '--color-primary');
      // Blue scheme uses @theme defaults, others set explicit values
      if (id !== 'blue') {
        expect(primaryColor, `Expected --color-primary for "${id}" to be "${color}"`).toBe(color);
      }
    }
  });

  test('scheme persists across page reload', async ({ page }) => {
    await navigateToSettings(page);

    // Select violet scheme
    await selectScheme(page, 'settings.appearance.schemeViolet');
    expect(await getDataScheme(page)).toBe('violet');

    // Reload the page
    await page.reload({ waitUntil: 'domcontentloaded' });

    // Verify scheme is restored from localStorage
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('violet');

    const primaryColor = await getCSSVariable(page, '--color-primary');
    expect(primaryColor).toBe('#7c3aed');
  });

  test('scheme persists when navigating to different pages', async ({ page }) => {
    await navigateToSettings(page);

    // Select amber scheme
    await selectScheme(page, 'settings.appearance.schemeAmber');
    expect(await getDataScheme(page)).toBe('amber');

    // Navigate to dashboard
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded');

    // Verify scheme persists
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('amber');
  });

  test('localStorage stores the selected scheme', async ({ page }) => {
    await navigateToSettings(page);

    await selectScheme(page, 'settings.appearance.schemeRose');

    const stored = await page.evaluate(() => localStorage.getItem('color-scheme'));
    expect(stored).toBe('rose');
  });
});

test.describe('Color Scheme with Dark Mode', () => {
  test.setTimeout(30000);

  test.beforeEach(async ({ page }) => {
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await clearSchemeStorage(page);
  });

  test('scheme works in dark mode', async ({ page }) => {
    // Set dark theme
    await page.evaluate(() => {
      localStorage.setItem('theme', 'dark');
      document.documentElement.setAttribute('data-theme', 'dark');
      document.documentElement.setAttribute('data-theme-controller', 'dark');
    });

    await navigateToSettings(page);

    // Select forest scheme
    await selectScheme(page, 'settings.appearance.schemeForest');

    const scheme = await getDataScheme(page);
    expect(scheme).toBe('forest');

    // In dark mode, forest uses a lighter green (#059669)
    const primaryColor = await getCSSVariable(page, '--color-primary');
    expect(primaryColor).toBe('#059669');
  });

  test('scheme overrides dark mode defaults', async ({ page }) => {
    // Set dark theme
    await page.evaluate(() => {
      localStorage.setItem('theme', 'dark');
      document.documentElement.setAttribute('data-theme', 'dark');
      document.documentElement.setAttribute('data-theme-controller', 'dark');
    });

    await navigateToSettings(page);

    // The default dark blue primary is #3b82f6
    // After switching to rose, it should change
    await selectScheme(page, 'settings.appearance.schemeRose');

    const primaryColor = await getCSSVariable(page, '--color-primary');
    // Dark rose should be #fb7185, not the dark blue default #3b82f6
    expect(primaryColor).not.toBe('#3b82f6');
    expect(primaryColor).toBe('#fb7185');
  });

  test('switching theme preserves color scheme', async ({ page }) => {
    await navigateToSettings(page);

    // Select violet in light mode
    await selectScheme(page, 'settings.appearance.schemeViolet');
    expect(await getDataScheme(page)).toBe('violet');

    // Switch to dark mode
    await page.evaluate(() => {
      localStorage.setItem('theme', 'dark');
      document.documentElement.setAttribute('data-theme', 'dark');
      document.documentElement.setAttribute('data-theme-controller', 'dark');
    });

    // Scheme should still be violet
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('violet');

    // Primary should be the dark violet value
    const primaryColor = await getCSSVariable(page, '--color-primary');
    expect(primaryColor).toBe('#8b5cf6');
  });
});

test.describe('Custom Color Scheme', () => {
  test.setTimeout(30000);

  test.beforeEach(async ({ page }) => {
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await clearSchemeStorage(page);
  });

  test('selecting custom scheme shows color pickers', async ({ page }) => {
    await navigateToSettings(page);

    await selectScheme(page, 'settings.appearance.schemeCustom');

    // Custom color picker panel should appear
    const primaryInput = page.locator(
      'input[type="color"][aria-label="settings.appearance.customPrimary"]'
    );
    const accentInput = page.locator(
      'input[type="color"][aria-label="settings.appearance.customAccent"]'
    );

    await expect(primaryInput).toBeVisible();
    await expect(accentInput).toBeVisible();
  });

  test('custom scheme uses custom CSS variables', async ({ page }) => {
    await navigateToSettings(page);

    // Set custom colors via localStorage before selecting custom scheme
    await page.evaluate(() => {
      localStorage.setItem(
        'custom-scheme-colors',
        JSON.stringify({
          primary: '#ff5500',
          accent: '#00aa55',
        })
      );
    });

    await selectScheme(page, 'settings.appearance.schemeCustom');

    const scheme = await getDataScheme(page);
    expect(scheme).toBe('custom');

    // Verify custom CSS variables are set
    const customPrimary = await getCSSVariable(page, '--custom-primary');
    expect(customPrimary).toBe('#ff5500');
  });

  test('custom scheme persists across reload', async ({ page }) => {
    await navigateToSettings(page);

    // Set custom colors and select custom scheme
    await page.evaluate(() => {
      localStorage.setItem(
        'custom-scheme-colors',
        JSON.stringify({
          primary: '#cc3366',
          accent: '#3366cc',
        })
      );
    });

    await selectScheme(page, 'settings.appearance.schemeCustom');
    expect(await getDataScheme(page)).toBe('custom');

    // Reload page
    await page.reload({ waitUntil: 'domcontentloaded' });

    // Verify custom scheme is restored
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('custom');

    const customPrimary = await getCSSVariable(page, '--custom-primary');
    expect(customPrimary).toBe('#cc3366');
  });
});

test.describe('Color Scheme Accessibility', () => {
  test.setTimeout(30000);

  test('scheme picker uses radiogroup pattern', async ({ page }) => {
    await navigateToSettings(page);

    // Should have a radiogroup
    const radiogroup = page.locator('[role="radiogroup"]');
    await expect(radiogroup).toBeVisible();

    // Should have radio buttons for each scheme (blue, forest, amber, violet, rose, custom)
    const radios = radiogroup.locator('button[role="radio"]');
    await expect(radios).toHaveCount(6);
  });

  test('selected scheme has aria-checked true', async ({ page }) => {
    await navigateToSettings(page);

    // Select forest
    await selectScheme(page, 'settings.appearance.schemeForest');

    const forestButton = page.locator(
      'button[role="radio"][aria-label="settings.appearance.schemeForest"]'
    );
    await expect(forestButton).toHaveAttribute('aria-checked', 'true');

    // Blue should no longer be checked
    const blueButton = page.locator(
      'button[role="radio"][aria-label="settings.appearance.schemeBlue"]'
    );
    await expect(blueButton).toHaveAttribute('aria-checked', 'false');
  });
});

test.describe('FOUC Prevention', () => {
  test.setTimeout(30000);

  test('scheme is applied before page content loads', async ({ page }) => {
    // Set a non-default scheme in localStorage before navigating
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.evaluate(() => {
      localStorage.setItem('color-scheme', 'rose');
    });

    // Navigate and check that data-scheme is set early (before JS framework hydration)
    await page.goto('/ui/settings', { timeout: 15000 });

    // The blocking script in index.html should set data-scheme before any content renders
    const scheme = await getDataScheme(page);
    expect(scheme).toBe('rose');
  });

  test('invalid localStorage value falls back to blue', async ({ page }) => {
    await page.goto('/ui/dashboard', { timeout: 15000 });
    await page.evaluate(() => {
      localStorage.setItem('color-scheme', 'invalid-scheme');
    });

    await page.goto('/ui/settings', { timeout: 15000 });

    const scheme = await getDataScheme(page);
    expect(scheme).toBe('blue');
  });
});

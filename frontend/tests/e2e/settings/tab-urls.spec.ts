/* eslint playwright/expect-expect: ['warn', { assertFunctionNames: ['expect', 'expectTab', 'expectQuery'] }] */
import { test, expect, type Page } from '@playwright/test';
import type { SettingsFormData } from '../../../src/lib/stores/settings';

const SETTINGS_BASE = `${process.env['SETTINGS_PROXY_PREFIX'] ?? ''}/ui/settings`;

const settingsPages = [
  { path: 'main', tabs: ['general', 'location', 'database'] },
  { path: 'audio', tabs: ['soundcard', 'streams', 'recording', 'retention', 'processing'] },
  {
    path: 'species',
    tabs: ['active', 'include', 'exclude', 'config', 'synonyms', 'tracking', 'dynamicThreshold'],
  },
  { path: 'analysis', tabs: ['settings', 'models'] },
  { path: 'integrations', tabs: ['birdweather', 'mqtt', 'ebird', 'prometheus'] },
  { path: 'security', tabs: ['server', 'basic-auth', 'oauth', 'exceptions', 'terminal'] },
  { path: 'detectionfilters', tabs: ['filters'] },
  { path: 'support', tabs: ['diagnostics'] },
  { path: 'userinterface', tabs: ['appearance', 'language', 'visualContent', 'audioPlayback'] },
  { path: 'notifications', tabs: ['channels', 'rules', 'history'] },
];

async function expectTab(page: Page, id: string) {
  const tab = page.locator(`#settings-tab-${id}`);
  await expect(tab).toHaveAttribute('aria-selected', 'true');
  await expect(tab).toHaveAttribute('tabindex', '0');
  await expect(tab).toHaveAccessibleName(/\S/);
  const panel = page.locator(`#settings-tabpanel-${id}`);
  await expect(panel).toBeVisible();
  await expect(panel).toHaveAttribute('aria-labelledby', `settings-tab-${id}`);
}

async function expectQuery(page: Page, key: string, value: string | null) {
  await expect.poll(() => new URL(page.url()).searchParams.get(key)).toBe(value);
}

test.describe('Settings tab URLs', () => {
  let runtimeErrors: string[];
  let consoleErrors: string[];

  test.beforeEach(async ({ page }) => {
    runtimeErrors = [];
    consoleErrors = [];
    page.on('pageerror', error => runtimeErrors.push(error.message));
    page.on('console', message => {
      if (message.type() === 'error') consoleErrors.push(message.text());
    });
    await page.addInitScript(() => localStorage.setItem('birdnet-locale', 'en'));
  });

  test.afterEach(async ({}, testInfo) => {
    if (consoleErrors.length) {
      await testInfo.attach('console-errors', {
        body: JSON.stringify(consoleErrors, null, 2),
        contentType: 'application/json',
      });
    }
    expect(runtimeErrors).toEqual([]);
  });

  for (const { path, tabs } of settingsPages) {
    test(`${path}: every tab supports direct load, refresh and same-page selection`, async ({
      page,
    }) => {
      test.setTimeout(120_000);
      for (const id of tabs) {
        await page.goto(`${SETTINGS_BASE}/${path}?tab=${id}&source=tab-test#settings`);
        await expectTab(page, id);
        await page.reload();
        await expectTab(page, id);
      }
      // Start from a bare URL in the same session: no new last-tab storage.
      await page.goto(`${SETTINGS_BASE}/${path}?source=tab-test#settings`);
      await expectTab(page, tabs[0]);
      for (const id of tabs) {
        await page.locator(`#settings-tab-${id}`).click();
        await expectTab(page, id);
        await expectQuery(page, 'tab', id);
        await expectQuery(page, 'source', 'tab-test');
        expect(new URL(page.url()).hash).toBe('#settings');
      }
    });

    test(`${path}: invalid tab falls back safely`, async ({ page }) => {
      await page.goto(`${SETTINGS_BASE}/${path}?tab=removed-tab`);
      await expectTab(page, tabs[0]);
    });
  }

  test('Back/Forward restores tabs and bare defaults without losing unsaved edits', async ({
    page,
  }) => {
    await page.goto(`${SETTINGS_BASE}/main?source=history#settings`);
    const name = page.locator('#node-name');
    await expect(name).toBeVisible();
    const original = await name.inputValue();
    await name.fill(`${original} tab test`);
    await name.press('Tab');
    const reset = page.getByRole('button', { name: 'Reset all changes' });
    await expect(reset).toBeVisible();
    await page.locator('#settings-tab-location').click();
    await expectTab(page, 'location');
    await expect(page.locator('#location-map canvas')).toBeVisible();
    await page.locator('#settings-tab-database').click();
    await expectTab(page, 'database');
    const historyLength = await page.evaluate(() => window.history.length);
    await page.locator('#settings-tab-database').click();
    expect(await page.evaluate(() => window.history.length)).toBe(historyLength);
    await page.goBack();
    await expectTab(page, 'location');
    await page.goBack();
    await expectTab(page, 'general');
    await expectQuery(page, 'tab', null);
    await expect(name).toHaveValue(`${original} tab test`);
    await page.goForward();
    await expectTab(page, 'location');
    await expect(reset).toBeVisible();
    await reset.click();
    await page.locator('#settings-tab-general').click();
    await expect(name).toHaveValue(original);
    await name.fill(`${original} refresh test`);
    await page.locator('#settings-tab-location').click();
    await page.reload();
    await expectTab(page, 'location');
    await page.locator('#settings-tab-general').click();
    await expect(name).toHaveValue(original);
  });

  test('Analysis nested tabs keep independent URLs through remounts and history', async ({
    page,
  }) => {
    await page.goto(`${SETTINGS_BASE}/analysis?tab=models&modelTab=available`);
    await expectTab(page, 'models');
    await expectTab(page, 'available');
    await page.reload();
    await expectTab(page, 'available');
    await page.locator('#settings-tab-installed').click();
    await expectQuery(page, 'tab', 'models');
    await expectQuery(page, 'modelTab', 'installed');
    await page.goBack();
    await expectTab(page, 'available');
    await page.goForward();
    await expectTab(page, 'installed');
    await page.locator('#settings-tab-settings').click();
    await page.locator('#settings-tab-models').click();
    await expectTab(page, 'installed');
    await page.goto(`${SETTINGS_BASE}/analysis?tab=models&modelTab=removed`);
    await expectTab(page, 'installed');
    await page.locator('#settings-tab-installed').focus();
    await page.keyboard.press('ArrowRight');
    await expectTab(page, 'available');
    await expect(page.locator('#settings-tab-available')).toBeFocused();
    await expectQuery(page, 'tab', 'models');
    await expectQuery(page, 'modelTab', 'available');
  });

  test('saving edits from another tab retains the URL and persists the form', async ({ page }) => {
    await page.goto(`${SETTINGS_BASE}/main?tab=general`);
    const name = page.locator('#node-name');
    await expect(name).toBeEnabled();
    const original = await name.inputValue();
    const updated = `${original} tab save test`;
    const save = page.getByRole('button', { name: 'Save Changes', exact: true });
    try {
      await name.fill(updated);
      await page.locator('#settings-tab-location').click();
      await expect(save).toBeEnabled();
      const saved = page.waitForResponse(
        response =>
          response.url().endsWith('/api/v2/settings') && response.request().method() === 'PUT'
      );
      await save.click();
      expect((await saved).ok()).toBe(true);
      await page.reload();
      await expectTab(page, 'location');
      await page.locator('#settings-tab-general').click();
      await expect(name).toHaveValue(updated);
    } finally {
      await page.goto(`${SETTINGS_BASE}/main?tab=general`);
      await name.fill(original);
      await name.press('Tab');
      if (await save.isEnabled()) {
        const restored = page.waitForResponse(
          response =>
            response.url().endsWith('/api/v2/settings') && response.request().method() === 'PUT'
        );
        await save.click();
        expect((await restored).ok()).toBe(true);
      }
    }
  });

  test('all settings tab groups remain usable on mobile', async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 390, height: 844 });
    for (const { path, tabs } of settingsPages) {
      await page.goto(`${SETTINGS_BASE}/${path}`);
      for (const id of tabs) {
        await page.locator(`#settings-tab-${id}`).click();
        await expectTab(page, id);
        await expectQuery(page, 'tab', id);
      }
      await page.screenshot({
        path: testInfo.outputPath(`${path}-mobile.png`),
        fullPage: true,
        animations: 'disabled',
      });
    }
  });

  for (const width of [1440, 390]) {
    test(`Species Configure Location preserves unsaved tracking edits at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      // Expose the missing-location entry point and an editable Tracking field without saving.
      await page.route('**/api/v2/settings', async route => {
        const response = await route.fetch();
        const settings: SettingsFormData = await response.json();
        settings.birdnet.locationConfigured = false;
        settings.realtime.speciesTracking = {
          ...settings.realtime.speciesTracking,
          enabled: true,
          newSpeciesWindowDays: 7,
        };
        return route.fulfill({ response, json: settings });
      });

      await page.goto(`${SETTINGS_BASE}/species?tab=tracking`);
      const windowDays = page.locator('#new-species-window');
      await expect(windowDays).toHaveValue('7');
      await windowDays.fill('11');
      await windowDays.press('Tab');
      const reset = page.getByRole('button', { name: 'Reset all changes' });
      await expect(reset).toBeVisible();
      await page.locator('#settings-tab-active').click();
      const link = page.getByRole('link', { name: 'Configure Location' });
      await expect(link).toHaveAttribute('href', `${SETTINGS_BASE}/main?tab=location`);
      await link.focus();
      await page.screenshot({
        path: testInfo.outputPath(`species-location-${width}.png`),
        fullPage: true,
        animations: 'disabled',
      });
      if (width === 1440) await link.click();
      else await link.press('Enter');
      await expectTab(page, 'location');
      await expect(page.locator('#location-map canvas')).toBeVisible();
      await expect(reset).toBeVisible();
      await page.goBack();
      await expectTab(page, 'active');
      await page.goBack();
      await expectTab(page, 'tracking');
      await expect(windowDays).toHaveValue('11');
      await page.goForward();
      await expectTab(page, 'active');
      await page.goForward();
      await expectTab(page, 'location');
      await expect(reset).toBeVisible();
    });
  }

  for (const width of [1440, 390, 320]) {
    test(`keyboard and layout at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${SETTINGS_BASE}/species?tab=active`);
      await expectTab(page, 'active');
      await page.locator('#settings-tab-active').focus();
      await page.keyboard.press('ArrowRight');
      await expectTab(page, 'include');
      await expect(page.locator('#settings-tab-include')).toBeFocused();
      await expectQuery(page, 'tab', 'include');
      await page.keyboard.press('End');
      await expectTab(page, 'dynamicThreshold');
      await page.keyboard.press('ArrowRight');
      await expectTab(page, 'active');
      await page.keyboard.press('ArrowLeft');
      await expectTab(page, 'dynamicThreshold');
      await page.keyboard.press('Home');
      await expectTab(page, 'active');
      for (const key of ['Enter', 'Space']) {
        const before = await page.evaluate(() => window.history.length);
        await page.keyboard.press(key);
        expect(await page.evaluate(() => window.history.length)).toBe(before);
      }
      for (const tab of await page.getByRole('tab').all()) {
        await expect(tab).toHaveAccessibleName(/\S/);
        const bounds = await tab.boundingBox();
        expect(bounds).not.toBeNull();
        expect(bounds?.x).toBeGreaterThanOrEqual(0);
        expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(width);
      }
      await page.screenshot({
        path: testInfo.outputPath(`species-${width}.png`),
        fullPage: true,
        animations: 'disabled',
      });
    });
  }

  test('explicit Audio URL overrides its legacy storage fallback', async ({ page }) => {
    await page.addInitScript(() =>
      localStorage.setItem('birdnet-audio-settings-active-tab', 'streams')
    );
    await page.goto(`${SETTINGS_BASE}/audio?tab=recording`);
    await expectTab(page, 'recording');
    await page.goto(`${SETTINGS_BASE}/audio`);
    await expectTab(page, 'streams');
  });
});

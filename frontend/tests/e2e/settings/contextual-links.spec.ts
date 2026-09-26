import { test, expect, type Page } from '@playwright/test';
import type { SettingsFormData } from '../../../src/lib/stores/settings';

const APP_BASE = `${process.env['SETTINGS_PROXY_PREFIX'] ?? ''}/ui`;

async function expectDestination(page: Page, section: string, tab: string) {
  await expect.poll(() => new URL(page.url()).pathname).toBe(`${APP_BASE}/settings/${section}`);
  await expect.poll(() => new URL(page.url()).search).toBe(`?tab=${tab}`);
  await expect(page.locator(`#settings-tab-${tab}`)).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator(`#settings-tabpanel-${tab}`)).toBeVisible();
}

test.describe('Contextual settings links', () => {
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
    // Exercise real page entry points without changing the running server's configuration.
    await page.route('**/api/v2/settings', async route => {
      const response = await route.fetch();
      const settings: SettingsFormData = await response.json();
      await route.fulfill({
        response,
        json: {
          ...settings,
          security: { ...settings.security, host: '', baseUrl: '', oauthProviders: [] },
          birdnet: { ...settings.birdnet, modelRegion: 'auto', locationConfigured: false },
          webServer: { ...settings.webServer, enableTerminal: false },
        },
      });
    });
    await page.route('**/api/v2/models/regions', async route => {
      const response = await route.fetch();
      await route.fulfill({
        response,
        json: {
          ...(await response.json()),
          modelRegion: 'auto',
          locationConfigured: false,
          resolved: { slug: '', source: 'auto', ambiguous: false },
        },
      });
    });
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

  for (const width of [1440, 390]) {
    test(`guidance links support keyboard navigation at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      const links = [
        {
          source: 'settings/analysis?tab=models',
          name: 'Configure Location',
          section: 'main',
          tab: 'location',
        },
        {
          source: 'system/terminal',
          name: 'Security → Browser Terminal',
          section: 'security',
          tab: 'terminal',
        },
        {
          source: 'settings/species?tab=dynamicThreshold',
          name: 'Analysis → Settings',
          section: 'analysis',
          tab: 'settings',
        },
      ];
      for (const { source, name, section, tab } of links) {
        await page.goto(`${APP_BASE}/${source}`);
        const link = page.getByRole('link', { name, exact: true });
        await expect(link).toBeVisible();
        await expect(link).toHaveAttribute('href', `${APP_BASE}/settings/${section}?tab=${tab}`);
        const bounds = await link.evaluate(element => {
          const { x, right } = element.getBoundingClientRect();
          return { x, right };
        });
        expect(bounds.x).toBeGreaterThanOrEqual(0);
        expect(bounds.right).toBeLessThanOrEqual(width);
        await page.screenshot({
          path: testInfo.outputPath(`${section}-${width}.png`),
          fullPage: true,
          animations: 'disabled',
        });
        await page.evaluate(() => {
          document.documentElement.dataset['navigationSession'] = 'retained';
        });
        await link.focus();
        await link.press('Enter');
        await expectDestination(page, section, tab);
        await expect(page.locator('html')).toHaveAttribute('data-navigation-session', 'retained');
        if (tab === 'location') await expect(page.locator('.maplibregl-canvas')).toBeVisible();
      }
    });

    test(`OAuth host link preserves the unfinished provider at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.goto(`${APP_BASE}/settings/security?tab=oauth`);
      await page.getByRole('button', { name: 'Add Provider', exact: true }).click();
      const clientId = page.getByLabel('Client ID', { exact: true });
      const userId = page.locator('#oauth-user-id');
      await clientId.fill('tab-navigation-test');
      await userId.fill('test-user');
      await userId.press('Tab');
      const link = page.getByRole('link', { name: 'Server Configuration', exact: true });
      await expect(link).toHaveAttribute('href', `${APP_BASE}/settings/security?tab=server`);
      await page.screenshot({
        path: testInfo.outputPath(`oauth-${width}.png`),
        fullPage: true,
        animations: 'disabled',
      });
      await link.click();
      await expectDestination(page, 'security', 'server');
      await page.goBack();
      await expectDestination(page, 'security', 'oauth');
      await expect(clientId).toHaveValue('tab-navigation-test');
      await expect(userId).toHaveValue('test-user');
      await link.focus();
      await link.press('Enter');
      await expectDestination(page, 'security', 'server');
      await page.locator('#settings-tab-oauth').click();
      await expect(clientId).toHaveValue('tab-navigation-test');
      await expect(userId).toHaveValue('test-user');
    });
  }

  test('a configured location outside coverage does not show the missing-location action', async ({
    page,
  }) => {
    await page.unroute('**/api/v2/models/regions');
    await page.route('**/api/v2/models/regions', async route => {
      const response = await route.fetch();
      await route.fulfill({
        response,
        json: {
          ...(await response.json()),
          modelRegion: 'auto',
          locationConfigured: true,
          resolved: { slug: '', source: 'auto', ambiguous: false },
        },
      });
    });
    await page.goto(`${APP_BASE}/settings/analysis?tab=models`);
    await expect(page.getByRole('group', { name: 'Model region', exact: true })).toBeVisible();
    await expect(
      page.getByText(
        "Your location is outside every regional model's coverage, so the worldwide model is used.",
        { exact: true }
      )
    ).toBeVisible();
    await expect(page.getByRole('link', { name: 'Configure Location', exact: true })).toHaveCount(
      0
    );
  });
});

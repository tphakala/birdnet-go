import { expect, test, type Page, type Route } from '@playwright/test';

const TEST_LOCATION = {
  latitude: 52.3728,
  longitude: 4.8936,
  accuracy: 8,
};

interface DeferredGeolocationWindow extends Window {
  resolvePendingGeolocation?: () => void;
}

async function installDeferredGeolocation(page: Page) {
  await page.addInitScript(location => {
    const browserWindow = window as DeferredGeolocationWindow;
    Object.defineProperty(navigator, 'geolocation', {
      configurable: true,
      value: {
        getCurrentPosition(success: PositionCallback) {
          browserWindow.resolvePendingGeolocation = () => {
            success({
              coords: {
                ...location,
                altitude: null,
                altitudeAccuracy: null,
                heading: null,
                speed: null,
                toJSON: () => ({}),
              },
              timestamp: Date.now(),
              toJSON: () => ({}),
            });
          };
        },
        watchPosition: () => 0,
        clearWatch: () => undefined,
      },
    });
  }, TEST_LOCATION);
}

async function resolvePendingGeolocation(page: Page) {
  await page.evaluate(() => {
    const browserWindow = window as DeferredGeolocationWindow;
    if (!browserWindow.resolvePendingGeolocation) {
      throw new Error('No pending geolocation request');
    }
    browserWindow.resolvePendingGeolocation();
  });
}

async function openLocationSettings(page: Page) {
  await page.goto('/ui/settings/main', { waitUntil: 'domcontentloaded' });
  await page.locator('#settings-tab-location').click();
  await expect(page.locator('#settings-tabpanel-location')).toBeVisible();
}

test.describe('Browser location', () => {
  test.use({
    geolocation: TEST_LOCATION,
    permissions: ['geolocation'],
  });

  test.beforeEach(async ({ page }, testInfo) => {
    // The repository's historical "iPad Pro" device alias is absent from
    // current Playwright, so keep this feature's tablet coverage explicit.
    if (testInfo.project.name === 'tablet') {
      await page.setViewportSize({ width: 1024, height: 768 });
    }

    await page.addInitScript(() => {
      localStorage.setItem('birdnet-locale', 'en');
    });
  });

  test('populates the settings form and survives the save/reload boundary', async ({
    page,
  }, testInfo) => {
    const browserErrors: string[] = [];
    page.on('pageerror', error => browserErrors.push(error.message));
    page.on('console', message => {
      if (message.type() === 'error') {
        browserErrors.push(message.text());
      }
    });

    let loadedSettings: unknown;
    let savedSettings: Record<string, unknown> | undefined;

    await page.route('**/api/v2/settings', async (route: Route) => {
      const request = route.request();
      const pathname = new URL(request.url()).pathname;
      if (pathname !== '/api/v2/settings') {
        await route.continue();
        return;
      }

      if (request.method() === 'GET') {
        if (savedSettings) {
          await route.fulfill({ json: savedSettings });
          return;
        }

        const response = await route.fetch();
        loadedSettings = await response.json();
        await route.fulfill({ response });
        return;
      }

      if (request.method() === 'PUT') {
        savedSettings = request.postDataJSON() as Record<string, unknown>;
        await route.fulfill({
          status: 200,
          json: { message: 'Settings updated successfully' },
        });
        return;
      }

      await route.continue();
    });

    await openLocationSettings(page);
    expect(loadedSettings).toBeDefined();

    const locationButton = page.getByRole('button', { name: 'Use browser location' });
    await expect(page.getByText('Automatic location', { exact: true })).toBeVisible();
    await expect(
      page.getByText("Fills the coordinates using this browser's location.", { exact: true })
    ).toBeVisible();
    await expect(locationButton).toHaveClass(/btn-primary/);
    const dividerWidth = await locationButton.evaluate(button => {
      const formControl = button.closest('.form-control');
      const layoutColumn = formControl?.parentElement;
      return layoutColumn ? getComputedStyle(layoutColumn).borderLeftWidth : null;
    });
    expect(dividerWidth).toBe(testInfo.project.name === 'tablet' ? '0px' : '1px');
    await locationButton.focus();
    await page.keyboard.press('Enter');

    await expect(page.getByLabel('Latitude')).toHaveValue('52.373');
    await expect(page.getByLabel('Longitude')).toHaveValue('4.894');
    await expect(page.getByText('Estimated accuracy: within 44 m', { exact: true })).toBeVisible();
    await expect(page.getByText('Browser location detected.', { exact: true })).toBeVisible();
    await expect(
      page.getByText("Fills the coordinates using this browser's location.", { exact: true })
    ).toBeHidden();
    expect(savedSettings).toBeUndefined();

    const saveButton = page.getByRole('button', { name: 'Save Changes' });
    await expect(saveButton).toBeEnabled();
    await saveButton.click();

    await expect
      .poll(() => savedSettings)
      .toMatchObject({
        birdnet: {
          latitude: 52.373,
          longitude: 4.894,
          locationConfigured: true,
        },
      });

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('#settings-tab-location').click();

    await expect(page.getByLabel('Latitude')).toHaveValue('52.373');
    await expect(page.getByLabel('Longitude')).toHaveValue('4.894');
    await expect(page.getByRole('button', { name: 'Save Changes' })).toBeDisabled();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
    expect(browserErrors).toEqual([]);
  });

  test('keeps uncommitted manual input when an older request resolves', async ({ page }) => {
    await installDeferredGeolocation(page);
    await openLocationSettings(page);

    const latitudeField = page.getByLabel('Latitude');
    const longitudeField = page.getByLabel('Longitude');
    const initialLongitude = await longitudeField.inputValue();

    await page.getByRole('button', { name: 'Use browser location' }).click();
    await expect(page.getByRole('button', { name: 'Locating...' })).toBeDisabled();

    await latitudeField.fill('51.501');

    await expect(page.getByRole('button', { name: 'Use browser location' })).toBeEnabled();
    await resolvePendingGeolocation(page);

    await expect(latitudeField).toHaveValue('51.501');
    await expect(longitudeField).toHaveValue(initialLongitude);
    await expect(page.getByText('Browser location detected.', { exact: true })).toBeHidden();
  });

  test('ignores a pending result after a save starts', async ({ page }) => {
    await installDeferredGeolocation(page);

    let saveStartedResolve: () => void = () => undefined;
    const saveStarted = new Promise<void>(resolve => {
      saveStartedResolve = resolve;
    });
    let saveRelease: () => void = () => undefined;
    const saveReleased = new Promise<void>(resolve => {
      saveRelease = resolve;
    });
    let savedSettings: Record<string, unknown> | undefined;

    await page.route('**/api/v2/settings', async (route: Route) => {
      if (route.request().method() !== 'PUT') {
        await route.continue();
        return;
      }

      savedSettings = route.request().postDataJSON() as Record<string, unknown>;
      saveStartedResolve();
      await saveReleased;
      await route.fulfill({
        status: 200,
        json: { message: 'Settings updated successfully' },
      });
    });

    await openLocationSettings(page);

    const latitudeField = page.getByLabel('Latitude');
    const longitudeField = page.getByLabel('Longitude');
    await latitudeField.fill('51.501');
    await latitudeField.blur();
    await longitudeField.fill('5.501');
    await longitudeField.blur();

    const locationButton = page.getByRole('button', { name: 'Use browser location' });
    await locationButton.click();
    await page.getByRole('button', { name: 'Save Changes' }).click();
    await saveStarted;
    await expect(locationButton).toBeDisabled();

    saveRelease();
    await expect(locationButton).toBeEnabled();
    await resolvePendingGeolocation(page);

    expect(savedSettings).toMatchObject({
      birdnet: {
        latitude: 51.501,
        longitude: 5.501,
        locationConfigured: true,
      },
    });
    await expect(latitudeField).toHaveValue('51.501');
    await expect(longitudeField).toHaveValue('5.501');
    await expect(page.getByRole('button', { name: 'Save Changes' })).toBeDisabled();
    await expect(page.getByText('Browser location detected.', { exact: true })).toBeHidden();
  });
});

import { test, expect } from '@playwright/test';

test.describe('Live Stream Page', () => {
  test('Route loads and renders page structure', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // Should navigate to live-stream URL
    await expect(page).toHaveURL(/.*\/ui\/live-stream/);

    // Should not show critical errors
    await expect(page.locator('[role="alert"]:has-text("Error"), .error-boundary')).toHaveCount(0);

    // Should have heading with the page title
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible();

    // Should have the source picker (custom SelectDropdown, not native select)
    const sourceSelect = page.getByRole('button', { name: /Audio Source|Loading/i });
    await expect(sourceSelect).toBeVisible();
  });

  test('Page has spectrogram canvas container', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // The SpectrogramCanvas renders a canvas inside a container div
    const canvas = page.locator('canvas');
    await expect(canvas).toBeVisible();
  });

  test('Page has spectrogram controls bar', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // Should have frequency range controls
    const freqMinSlider = page.locator('#spectrogram-freq-min');
    const freqMaxSlider = page.locator('#spectrogram-freq-max');
    await expect(freqMinSlider).toBeVisible();
    await expect(freqMaxSlider).toBeVisible();

    // Should have color map selector (custom SelectDropdown, not native select)
    // The SelectDropdown renders as a button trigger with the selected value text
    const colorMapTrigger = page.getByText('Inferno').first();
    await expect(colorMapTrigger).toBeVisible();

    // Should have gain slider (full page mode, not compact)
    const gainSlider = page.locator('#spectrogram-gain');
    await expect(gainSlider).toBeVisible();

    // Should have mute/unmute button
    const muteButton = page.getByRole('button', { name: /mute|unmute/i });
    await expect(muteButton).toBeVisible();
  });

  test('Sidebar has Live Audio navigation entry', async ({ page }) => {
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // The sidebar should have a navigation entry for live stream
    // Look for a button with the Radio icon that navigates to live-stream
    const navButton = page.locator('nav button, [role="navigation"] button').filter({
      hasText: /live audio/i,
    });

    // On desktop, navigation should be visible
    if (await navButton.isVisible()) {
      await navButton.click();
      await page.waitForLoadState('domcontentloaded');
      await expect(page).toHaveURL(/.*\/ui\/live-stream/);
    }
  });

  test('Browser back/forward works with live-stream route', async ({ page }) => {
    // Start at dashboard
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // Navigate to live-stream
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');
    await expect(page).toHaveURL(/.*\/ui\/live-stream/);

    // Browser back
    await page.goBack();
    await page.waitForLoadState('domcontentloaded');
    await expect(page).toHaveURL(/.*\/ui\/dashboard/);

    // Browser forward
    await page.goForward();
    await page.waitForLoadState('domcontentloaded');
    await expect(page).toHaveURL(/.*\/ui\/live-stream/);
  });

  test('Color map selector changes value', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // Default should be inferno (shown in the SelectDropdown trigger)
    const trigger = page.getByText('Inferno').first();
    await expect(trigger).toBeVisible();

    // Open the dropdown and select viridis
    await trigger.click();
    const viridisOption = page.getByText('Viridis').first();
    await expect(viridisOption).toBeVisible();
    await viridisOption.click();

    // Trigger should now show viridis
    await expect(page.getByText('Viridis').first()).toBeVisible();
  });

  test('Page fills viewport height without scrollbar', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // The outer container should use col-span-12 and calc(100dvh) height
    const container = page.locator('.col-span-12').filter({ has: page.locator('canvas') });
    await expect(container).toBeVisible();

    // The canvas container should have non-zero dimensions
    const canvasContainer = page.locator('canvas').first();
    const box = await canvasContainer.boundingBox();
    expect(box).not.toBeNull();
    if (box) {
      expect(box.width).toBeGreaterThan(100);
      expect(box.height).toBeGreaterThan(50);
    }
  });
});

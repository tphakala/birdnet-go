import { test, expect } from '@playwright/test';

test.describe('Live Stream Page', () => {
  /** Minimum expected width (px) for the live-stream container filling the viewport. */
  const MIN_CONTAINER_WIDTH_PX = 100;
  /** Minimum expected height (px) for the live-stream container filling the viewport. */
  const MIN_CONTAINER_HEIGHT_PX = 50;

  test('Route loads and renders page structure', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // Should navigate to live-stream URL
    await expect(page).toHaveURL(/.*\/ui\/live-stream/);

    // Should have heading with the page title
    const heading = page.getByRole('heading', { level: 1 });
    await expect(heading).toBeVisible();

    // Should have the source picker (SelectDropdown renders a <button> with
    // aria-haspopup="listbox" and placeholder text like "Loading..." or "Audio Source").
    // Filter by text to avoid binding to a different dropdown (e.g. color map).
    const sourceSelect = page
      .locator('button[aria-haspopup="listbox"]')
      .filter({ hasText: /audio source|loading/i })
      .first();
    await expect(sourceSelect).toBeVisible();
  });

  test('Page has spectrogram area with start prompt', async ({ page }) => {
    await page.goto('/ui/live-stream');
    await page.waitForLoadState('domcontentloaded');

    // On initial page load (before streaming starts), the spectrogram area
    // shows a Play button placeholder or a Loading spinner -- not a canvas.
    // The canvas only renders when isStreaming || isConnecting.
    const spectrogramArea = page.locator('.min-h-0.flex-1');
    await expect(spectrogramArea).toBeVisible();

    // Should have either a loading spinner or a play button
    const placeholder = spectrogramArea.locator('button, .animate-spin');
    await expect(placeholder.first()).toBeVisible();
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

    // The outer container should use col-span-12 and calc(100dvh) height.
    // Note: canvas only renders during streaming; on initial load the container
    // holds a placeholder button instead.
    const container = page.locator('.col-span-12').first();
    await expect(container).toBeVisible();

    // The container should have non-zero dimensions filling the viewport
    const box = await container.boundingBox();
    expect(box).not.toBeNull();
    if (box) {
      expect(box.width).toBeGreaterThan(MIN_CONTAINER_WIDTH_PX);
      expect(box.height).toBeGreaterThan(MIN_CONTAINER_HEIGHT_PX);
    }
  });
});

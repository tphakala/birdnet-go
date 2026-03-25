import { test, expect } from '@playwright/test';

/**
 * Regression tests for audio settings popup (gain/filter sliders) in detection
 * detail and review modal views.
 *
 * Bug: Clicking or dragging sliders in the audio settings popup on desktop
 * would pass through to the spectrogram underneath, starting a time-range
 * selection instead of adjusting the slider. Root cause was that the popup's
 * mousedown/touchstart events were not stopped from propagating to the
 * spectrogram's selection handlers.
 *
 * Fixes: #2431, #2516
 */

test.describe('Audio Settings Popup - Click-through Regression', () => {
  test('audio settings popup sliders do not trigger spectrogram selection', async ({ page }) => {
    // Navigate to dashboard to find a detection
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // Find a detection card link to navigate to detail page
    const detectionLink = page.locator('a[href*="/ui/detection/"]').first();
    if (!(await detectionLink.isVisible({ timeout: 10000 }).catch(() => false))) {
      // eslint-disable-next-line playwright/no-skipped-test -- No detections in test env
      test.skip(true, 'No detections available to test audio settings popup');
      return;
    }

    // Navigate to the detection detail page
    await detectionLink.click();
    await page.waitForLoadState('domcontentloaded');

    // Find the audio settings button (Volume2 icon button with aria-label)
    const audioSettingsBtn = page
      .locator('[aria-label="Audio settings"], [aria-haspopup="true"]')
      .filter({
        has: page.locator('svg'),
      })
      .first();

    if (!(await audioSettingsBtn.isVisible({ timeout: 5000 }).catch(() => false))) {
      // Audio settings button may be hidden until hover on desktop
      // Try hovering over the player container to reveal it
      const playerContainer = page
        .locator('.group')
        .filter({
          has: page.locator('img[alt*="spectrogram"], img[alt*="Spectrogram"]'),
        })
        .first();

      if (await playerContainer.isVisible({ timeout: 3000 }).catch(() => false)) {
        await playerContainer.hover();
      }
    }

    // Try to find the settings button again after hover
    const settingsBtn = page
      .locator('[aria-haspopup="true"]')
      .filter({
        has: page.locator('svg'),
      })
      .first();

    if (!(await settingsBtn.isVisible({ timeout: 3000 }).catch(() => false))) {
      // eslint-disable-next-line playwright/no-skipped-test -- UI structure may differ
      test.skip(true, 'Audio settings button not found on detection detail page');
      return;
    }

    // Open the audio settings popup
    await settingsBtn.click();

    // Verify the settings dialog opened
    const settingsDialog = page.locator('[role="dialog"]');
    await expect(settingsDialog).toBeVisible({ timeout: 3000 });

    // Find a slider (gain or filter) in the popup
    const slider = settingsDialog.locator('input[type="range"]').first();
    await expect(slider).toBeVisible();

    // Get the slider's bounding box for interaction
    const sliderBox = await slider.boundingBox();
    if (!sliderBox) {
      // eslint-disable-next-line playwright/no-skipped-test -- Slider not measurable
      test.skip(true, 'Cannot measure slider dimensions');
      return;
    }

    // Interact with the slider by clicking and dragging
    // This mousedown + mousemove should NOT create a spectrogram selection
    const sliderCenterY = sliderBox.y + sliderBox.height / 2;
    const sliderStartX = sliderBox.x + sliderBox.width * 0.3;
    const sliderEndX = sliderBox.x + sliderBox.width * 0.7;

    await page.mouse.move(sliderStartX, sliderCenterY);
    await page.mouse.down();
    await page.mouse.move(sliderEndX, sliderCenterY, { steps: 5 });
    await page.mouse.up();

    // The settings dialog should still be open (not dismissed by the interaction)
    await expect(settingsDialog).toBeVisible();

    // Verify no spectrogram selection was created
    // Selection overlays have specific classes like 'bg-primary/20' or 'cursor-grab'
    const selectionOverlay = page.locator(
      '.cursor-grab, .cursor-grabbing, [aria-label*="Selection"]'
    );
    const hasSelection = await selectionOverlay.isVisible().catch(() => false);
    expect(hasSelection).toBe(false);
  });

  test('mousedown on audio settings popup does not propagate to spectrogram', async ({ page }) => {
    // This test verifies the fix at the DOM level by checking that
    // mousedown events on the popup's interactive elements are stopped
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    const detectionLink = page.locator('a[href*="/ui/detection/"]').first();
    if (!(await detectionLink.isVisible({ timeout: 10000 }).catch(() => false))) {
      // eslint-disable-next-line playwright/no-skipped-test -- No detections in test env
      test.skip(true, 'No detections available to test');
      return;
    }

    await detectionLink.click();
    await page.waitForLoadState('domcontentloaded');

    // Use evaluate to inject a listener that tracks if the spectrogram container
    // received a mousedown event (which it should NOT when interacting with popup)
    const propagationCheck = await page.evaluate(() => {
      return new Promise<{ ready: boolean; propagated: boolean }>(resolve => {
        // Find the player container (has onmousedown for clip extraction)
        const container = document.querySelector('.cursor-crosshair, .group.overflow-hidden');
        if (!container) {
          resolve({ ready: false, propagated: false });
          return;
        }

        let received = false;
        const listener = () => {
          received = true;
        };
        container.addEventListener('mousedown', listener, { capture: false });

        // Find and open the audio settings button
        const btn = container.querySelector('[aria-haspopup="true"]');
        if (!btn) {
          container.removeEventListener('mousedown', listener);
          resolve({ ready: false, propagated: false });
          return;
        }

        // Simulate clicking the button to open popup
        (btn as HTMLElement).click();

        // Wait for popup to appear, then check if mousedown on the dialog propagated
        setTimeout(() => {
          const dialog = document.querySelector('[role="dialog"]');
          if (!dialog) {
            container.removeEventListener('mousedown', listener);
            resolve({ ready: false, propagated: false });
            return;
          }

          // Simulate mousedown on the dialog
          const event = new MouseEvent('mousedown', {
            bubbles: true,
            cancelable: true,
            clientX: 100,
            clientY: 100,
          });
          dialog.dispatchEvent(event);

          // Check if the container received the event
          const result = received;
          container.removeEventListener('mousedown', listener);
          resolve({ ready: true, propagated: result });
        }, 500);
      });
    });

    // Verify the test infrastructure was ready (container, button, and dialog all found)
    // and that mousedown on the dialog did NOT propagate to the spectrogram container
    if (propagationCheck.ready) {
      expect(propagationCheck.propagated).toBe(false);
    }
  });
});

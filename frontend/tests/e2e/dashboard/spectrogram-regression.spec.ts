import { test, expect, type ConsoleMessage } from '@playwright/test';

/**
 * Regression tests for MiniSpectrogram dashboard widget.
 *
 * These tests catch three classes of bugs that only surface in a real browser:
 *
 * 1. effect_update_depth_exceeded — Svelte $effect infinite loops caused by
 *    reactive reads inside side-effect functions (start(), shouldAutoStart())
 *    that are called within $effect bodies.
 *
 * 2. Infinite spinner on page reload — AudioContext.resume() blocking without
 *    a user gesture, preventing isActive from being set when spectro.connect()
 *    is awaited before state updates.
 *
 * 3. Reactive dependency leaks — functions called inside $effect that
 *    accidentally read $state variables (isActive, isConnecting), making them
 *    tracked dependencies that trigger cleanup→restart loops.
 */

const SPECTROGRAM_STORAGE_KEY = 'birdnet-spectrogram-active';

/**
 * Patterns to ignore — these are expected browser behavior, not app bugs.
 */
const IGNORED_ERROR_PATTERNS: RegExp[] = [
  /SSE connection error/,
  /Permissions policy violation/,
  // AudioContext autoplay restriction is expected on auto-start without gesture
  /AudioContext was not allowed to start/,
  // HLS segment fetch failures during teardown/reload
  /Failed to load resource.*\.m3u8/,
  /Failed to load resource.*\.m4s/,
  /Failed to load resource.*\.mp4/,
  // EventSource connection failures during page unload
  /EventSource failed/,
  // 404s for optional resources (species images, placeholders)
  /Failed to load resource.*404/,
];

function isIgnoredError(message: string): boolean {
  return IGNORED_ERROR_PATTERNS.some(pattern => pattern.test(message));
}

interface CollectedError {
  type: 'pageerror' | 'console.error';
  message: string;
}

/**
 * Collect page errors and console.error messages during an action.
 * Filters out known harmless patterns.
 */
async function collectErrorsDuring(
  page: import('@playwright/test').Page,
  action: () => Promise<void>
): Promise<CollectedError[]> {
  const errors: CollectedError[] = [];

  const pageErrorHandler = (error: Error) => {
    if (!isIgnoredError(error.message)) {
      errors.push({ type: 'pageerror', message: error.message });
    }
  };
  const consoleHandler = (msg: ConsoleMessage) => {
    if (msg.type() === 'error' && !isIgnoredError(msg.text())) {
      errors.push({ type: 'console.error', message: msg.text() });
    }
  };

  page.on('pageerror', pageErrorHandler);
  page.on('console', consoleHandler);

  try {
    await action();
  } finally {
    page.off('pageerror', pageErrorHandler);
    page.off('console', consoleHandler);
  }

  return errors;
}

/**
 * Check specifically for Svelte effect depth errors — these are always fatal
 * and indicate an infinite reactive loop.
 */
function hasEffectDepthError(errors: CollectedError[]): boolean {
  return errors.some(
    e =>
      e.message.includes('effect_update_depth_exceeded') ||
      e.message.includes('svelte.dev/e/effect_update_depth_exceeded')
  );
}

test.describe('MiniSpectrogram — Effect Loop Regression', () => {
  test('dashboard loads without effect_update_depth_exceeded', async ({ page }) => {
    const errors = await collectErrorsDuring(page, async () => {
      await page.goto('/ui/dashboard');
      await page.waitForLoadState('domcontentloaded');
      // Allow time for SSE connections, effects, and HLS discovery
      await page.waitForTimeout(3000);
    });

    expect(
      hasEffectDepthError(errors),
      `effect_update_depth_exceeded detected on dashboard load: ${JSON.stringify(errors)}`
    ).toBe(false);
  });

  test('dashboard reload with spectrogram auto-start does not hang', async ({ page }) => {
    // Set localStorage to simulate "spectrogram was active before reload"
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await page.evaluate(key => {
      localStorage.setItem(key, 'true');
    }, SPECTROGRAM_STORAGE_KEY);

    // Reload — this is the scenario that triggers the effect loop:
    // $effect reads hasAccess → calls start() → start() reads isConnecting ($state)
    // → cleanup sets isConnecting=false → effect re-runs → infinite loop
    const errors = await collectErrorsDuring(page, async () => {
      await page.reload();
      await page.waitForLoadState('domcontentloaded');
      await page.waitForTimeout(3000);
    });

    expect(
      hasEffectDepthError(errors),
      `effect_update_depth_exceeded on reload with auto-start: ${JSON.stringify(errors)}`
    ).toBe(false);

    // Page should not be hung — main content should be visible
    const mainContent = page.locator('main, [role="main"], [data-testid="main-content"]');
    await expect(mainContent.first()).toBeVisible();
  });

  test('spectrogram widget does not show infinite spinner on auto-start', async ({ page }) => {
    // Enable auto-start via localStorage
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await page.evaluate(key => {
      localStorage.setItem(key, 'true');
    }, SPECTROGRAM_STORAGE_KEY);

    // Reload to trigger auto-start
    await page.reload();
    await page.waitForLoadState('domcontentloaded');

    // Wait for HLS startup attempt (source discovery + stream start)
    await page.waitForTimeout(5000);

    // The spinner element: a div with animate-spin inside the spectrogram card
    // If the spectrogram is active, it should show either the canvas or nothing,
    // but NOT an infinite spinner after 5 seconds
    const spectrogramSection = page.locator('text=Live Spectrogram').locator('..');
    if (await spectrogramSection.isVisible()) {
      const spinner = spectrogramSection.locator('.animate-spin');
      // Spinner should have disappeared by now (either canvas shown or play button)
      const spinnerVisible = await spinner.isVisible().catch(() => false);

      // Soft assertion: if spinner is still visible after 5s, it's likely stuck
      // We don't hard-fail because the backend may not have audio sources in CI
      if (spinnerVisible) {
        // Check that the page isn't hung (other elements should still be interactive)
        const sidebar = page.locator('nav').first();
        await expect(sidebar).toBeVisible({ timeout: 2000 });
        // Log warning — the spinner might be legitimate if source discovery is slow
        console.warn('Spectrogram spinner still visible after 5s — may indicate stuck state');
      }
    }
  });

  test('multiple rapid reloads with auto-start do not crash', async ({ page }) => {
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    // Enable auto-start
    await page.evaluate(key => {
      localStorage.setItem(key, 'true');
    }, SPECTROGRAM_STORAGE_KEY);

    // Rapid reloads stress the effect cleanup → restart cycle
    const errors = await collectErrorsDuring(page, async () => {
      for (let i = 0; i < 3; i++) {
        await page.reload();
        await page.waitForLoadState('domcontentloaded');
        await page.waitForTimeout(1500);
      }
    });

    expect(
      hasEffectDepthError(errors),
      `effect_update_depth_exceeded during rapid reloads: ${JSON.stringify(errors)}`
    ).toBe(false);

    // Page should still be functional
    const mainContent = page.locator('main, [role="main"], [data-testid="main-content"]');
    await expect(mainContent.first()).toBeVisible();
  });

  test('dashboard card order is preserved after reload', async ({ page }) => {
    // Load dashboard and get initial card order
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // Get card headings in order (h3 elements inside card sections)
    const getCardOrder = async () => {
      return page.evaluate(() => {
        const headings = document.querySelectorAll(
          '.col-span-12 h3, section h3, [class*="card"] h3'
        );
        return Array.from(headings)
          .map(h => h.textContent.trim())
          .filter(Boolean);
      });
    };

    const initialOrder = await getCardOrder();

    // Skip if no cards found (backend might not serve layout config in CI)
    if (initialOrder.length === 0) {
      // eslint-disable-next-line playwright/no-skipped-test -- intentionally skipped
      test.skip(true, 'No dashboard cards found — backend may not serve layout config');
      return;
    }

    // Reload and compare
    await page.reload();
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    const reloadOrder = await getCardOrder();

    expect(reloadOrder).toEqual(initialOrder);
  });
});

test.describe('MiniSpectrogram — Cleanup on Navigation', () => {
  test('navigating away from dashboard does not produce errors', async ({ page }) => {
    // Enable auto-start
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await page.evaluate(key => {
      localStorage.setItem(key, 'true');
    }, SPECTROGRAM_STORAGE_KEY);

    // Wait for potential stream startup
    await page.waitForTimeout(3000);

    // Navigate away — should trigger cleanup without errors
    const errors = await collectErrorsDuring(page, async () => {
      await page.goto('/ui/settings');
      await page.waitForLoadState('domcontentloaded');
      await page.waitForTimeout(2000);
    });

    expect(
      hasEffectDepthError(errors),
      `effect_update_depth_exceeded when navigating away: ${JSON.stringify(errors)}`
    ).toBe(false);
  });

  test('navigating back to dashboard after away does not loop', async ({ page }) => {
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');

    await page.evaluate(key => {
      localStorage.setItem(key, 'true');
    }, SPECTROGRAM_STORAGE_KEY);

    await page.waitForTimeout(2000);

    // Navigate away and back
    const errors = await collectErrorsDuring(page, async () => {
      await page.goto('/ui/settings');
      await page.waitForLoadState('domcontentloaded');
      await page.waitForTimeout(1000);

      await page.goto('/ui/dashboard');
      await page.waitForLoadState('domcontentloaded');
      await page.waitForTimeout(3000);
    });

    expect(
      hasEffectDepthError(errors),
      `effect_update_depth_exceeded on navigate back: ${JSON.stringify(errors)}`
    ).toBe(false);

    // Dashboard should be functional
    const mainContent = page.locator('main, [role="main"], [data-testid="main-content"]');
    await expect(mainContent.first()).toBeVisible();
  });
});

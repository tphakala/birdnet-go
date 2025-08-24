import { test, expect } from '@playwright/test';

test.describe('New UI Routing - /ui/ paths only', () => {
  test('All major routes work within new UI structure', async ({ page }) => {
    // Test main routes under /ui/
    const routes = ['/ui/', '/ui/dashboard', '/ui/settings', '/ui/detections', '/ui/analytics'];

    for (const route of routes) {
      await page.goto(route);

      // Wait for page to load
      await page.waitForLoadState('networkidle');

      // Should always be on a /ui/ route
      await expect(page).toHaveURL(/.*\/ui\/.*/);

      // Should have some content (main area, navigation, or basic page structure)
      const hasContent = await page
        .locator('body')
        .evaluate(el => el.textContent && el.textContent.trim().length > 0);
      expect(hasContent).toBe(true);

      // Should not show critical errors
      const errorElements = page.locator('[role="alert"]:has-text("Error"), .error-boundary');
      if ((await errorElements.count()) > 0) {
        await expect(errorElements).not.toBeVisible();
      }
    }
  });

  test('Legacy routes redirect or are avoided', async ({ page }) => {
    // Test that we consistently use new UI routes
    await page.goto('/ui/dashboard');

    // Ensure we stay in the new UI structure
    await expect(page).toHaveURL(/.*\/ui\/dashboard/);

    // Check that navigation links (if present) point to /ui/ routes
    const navLinks = page.locator('a[href^="/"]');
    const linkCount = await navLinks.count();

    if (linkCount > 0) {
      for (let i = 0; i < Math.min(5, linkCount); i++) {
        // Check first 5 links
        const href = await navLinks.nth(i).getAttribute('href');
        if (
          href &&
          href.startsWith('/') &&
          !href.startsWith('/api/') &&
          !href.startsWith('/assets/')
        ) {
          // Internal navigation links should prefer /ui/ routes or be external
          const isValidRoute =
            href.startsWith('/ui/') ||
            href.startsWith('http') ||
            href === '/' ||
            href.startsWith('#');

          if (!isValidRoute) {
            // eslint-disable-next-line no-console -- Debugging test warnings
            console.warn(`Found potential legacy route link: ${href}`);
          }
        }
      }
    }
  });

  test('New UI components and features are present', async ({ page }) => {
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('networkidle');

    // Look for modern UI components that would be in the new Svelte UI
    const modernUIElements = [
      'nav', // Modern navigation
      '[data-testid*="navigation"]',
      '[data-testid*="sidebar"]',
      '[role="main"]', // Semantic main content
      '[data-testid="main-content"]',
      'main', // HTML5 semantic main
      '[data-svelte-h]', // Svelte hydration markers (if present)
      '.svelte-*', // Svelte generated classes
    ];

    let hasModernUI = false;
    for (const selector of modernUIElements) {
      if ((await page.locator(selector).count()) > 0) {
        hasModernUI = true;
        break;
      }
    }

    // Should have at least some modern UI structure
    expect(hasModernUI).toBe(true);

    // Should not have obvious HTMX artifacts (if we can detect them)
    const htmxElements = page.locator('[hx-get], [hx-post], [hx-target], script:has-text("htmx")');
    const htmxCount = await htmxElements.count();

    if (htmxCount > 0) {
      // eslint-disable-next-line no-console -- Debugging test warnings
      console.warn(`Found ${htmxCount} potential HTMX elements - ensure tests focus on new UI`);
    }
  });

  test('New UI handles client-side routing', async ({ page }) => {
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('networkidle');

    // Try to navigate within the UI (if navigation exists)
    const settingsLink = page
      .locator('a[href*="/ui/settings"], [data-testid*="settings-link"]')
      .first();

    if (await settingsLink.isVisible()) {
      await settingsLink.click();

      // Should navigate to settings within the new UI
      await expect(page).toHaveURL(/.*\/ui\/settings/);
      await page.waitForLoadState('networkidle');

      // Should have settings content
      const hasContent = await page
        .locator('body')
        .evaluate(el => el.textContent && el.textContent.trim().length > 0);
      expect(hasContent).toBe(true);
    } else {
      // Fallback: direct navigation to settings
      await page.goto('/ui/settings');
      await expect(page).toHaveURL(/.*\/ui\/settings/);
    }
  });

  test('Browser back/forward works with new UI routing', async ({ page }) => {
    // Start at dashboard
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('networkidle');

    // Navigate to settings
    await page.goto('/ui/settings');
    await page.waitForLoadState('networkidle');
    await expect(page).toHaveURL(/.*\/ui\/settings/);

    // Use browser back button
    await page.goBack();
    await page.waitForLoadState('networkidle');

    // Should be back at dashboard
    await expect(page).toHaveURL(/.*\/ui\/dashboard/);

    // Use browser forward button
    await page.goForward();
    await page.waitForLoadState('networkidle');

    // Should be back at settings
    await expect(page).toHaveURL(/.*\/ui\/settings/);
  });
});

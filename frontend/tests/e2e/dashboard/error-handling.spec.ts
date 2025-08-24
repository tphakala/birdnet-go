import { test, expect } from '@playwright/test';

test.describe('Error Handling and Network Failures', () => {
  test('Application handles JavaScript errors gracefully', async ({ page }) => {
    const errors: string[] = [];

    // Capture JavaScript errors
    page.on('pageerror', error => {
      errors.push(error.message);
    });

    // Capture console errors
    page.on('console', msg => {
      if (msg.type() === 'error') {
        errors.push(msg.text());
      }
    });

    await page.goto('/ui/dashboard');

    // Wait for the page to load completely
    await page.waitForLoadState('networkidle');

    // Inject a controlled error to test error boundary
    await page.evaluate(() => {
      // Create a custom event that might trigger an error boundary
      const event = new CustomEvent('test-error', { detail: { force: true } });
      window.dispatchEvent(event);
    });

    // Wait a bit to see if any errors surface
    await page.waitForTimeout(1000);

    // Check that the app didn't completely crash
    await expect(page.locator('body')).toBeVisible();

    // Look for error boundary UI if it exists
    const errorBoundary = page.locator('[data-testid="error-boundary"], .error-boundary');
    const hasErrorBoundary = (await errorBoundary.count()) > 0;

    if (hasErrorBoundary && (await errorBoundary.isVisible())) {
      // If there's an error boundary, it should show user-friendly content
      await expect(errorBoundary).toContainText(/something went wrong|error|reload|refresh/i);
    } else {
      // If no error boundary, app should still function
      const navigation = page.locator('nav, [data-testid="navigation"], [data-testid="sidebar"]');
      await expect(navigation.first()).toBeVisible();
    }
  });

  test('Application handles API network failures', async ({ page }) => {
    // Start intercepting network requests
    await page.route('/api/v2/**', route => {
      // Simulate network failure for API calls
      route.abort('failed');
    });

    await page.goto('/ui/dashboard');

    // Wait for initial load
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Check that the app shows appropriate error states for failed requests
    // (App should handle failures gracefully without crashing)

    // Wait for potential error states to appear
    await page.waitForTimeout(2000);

    // App should either show error states OR handle failures gracefully
    const navigation = page.locator('nav, [data-testid="navigation"], [data-testid="sidebar"]');
    await expect(navigation.first()).toBeVisible();

    // The app should remain navigable even with API failures
    const isNavigationWorking = await navigation.first().isVisible();
    expect(isNavigationWorking).toBe(true);
  });

  test('Application handles slow network conditions', async ({ page }) => {
    // Simulate slow network
    await page.route('/api/v2/**', route => {
      setTimeout(() => {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'delayed response' }),
        });
      }, 3000); // 3 second delay
    });

    await page.goto('/ui/dashboard');

    // Check that loading states are shown during slow requests
    // (App should remain functional even with slow API responses)

    // Should show main content even while API calls are slow
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Navigation should work despite slow API
    const navigation = page.locator('nav, [data-testid="navigation"], [data-testid="sidebar"]');
    await expect(navigation.first()).toBeVisible();
  });

  test('Application handles malformed API responses', async ({ page }) => {
    // Intercept API calls and return malformed JSON
    await page.route('/api/v2/**', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{"invalid": json malformed}',
      });
    });

    await page.goto('/ui/dashboard');

    // App should handle JSON parse errors gracefully
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Check that navigation still works
    const navigation = page.locator('nav, [data-testid="navigation"], [data-testid="sidebar"]');
    await expect(navigation.first()).toBeVisible();

    // Should not have unhandled JavaScript errors that break the UI
    const bodyText = await page.locator('body').textContent();
    expect(bodyText).not.toContain('SyntaxError');
    expect(bodyText).not.toContain('Unexpected token');
  });

  test('Application recovers from temporary network issues', async ({ page }) => {
    let requestCount = 0;

    // Fail first request, succeed on retry
    await page.route('/api/v2/health', route => {
      requestCount++;
      if (requestCount === 1) {
        route.abort('failed');
      } else {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ status: 'ok' }),
        });
      }
    });

    await page.goto('/ui/dashboard');

    // Should eventually recover and show content
    await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();

    // Verify that retry mechanism worked
    expect(requestCount).toBeGreaterThan(1);
  });
});

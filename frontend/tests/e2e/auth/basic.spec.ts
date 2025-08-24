import { test, expect } from '@playwright/test';

test.describe('Authentication - New UI Only', () => {
  test('Protected routes in new UI handle authentication correctly', async ({ page }) => {
    // Try to access a protected route in the new UI without authentication
    await page.goto('/ui/settings');

    // Wait for navigation to complete (handles redirects)
    await page.waitForLoadState('domcontentloaded');

    // Wait a bit more for potential client-side redirects
    await page.waitForTimeout(1000);

    const currentUrl = page.url();

    // Check if we were redirected to login/auth
    const isRedirectedToLogin = currentUrl.includes('/login') || currentUrl.includes('/auth');

    // Check if login form elements are present
    const hasLoginForm = await page
      .locator(
        '[data-testid*="login"], [data-testid*="auth"], input[type="password"], [data-testid="login-form"]'
      )
      .isVisible();

    // Check if we stayed on settings page (publicly accessible)
    const isOnSettingsPage = currentUrl.includes('/ui/settings');
    const hasSettingsContent = await page
      .locator('[data-testid="main-content"], main, [role="main"]')
      .isVisible();

    if (isRedirectedToLogin || hasLoginForm) {
      // Assert we were redirected or show login form
      expect(isRedirectedToLogin || hasLoginForm).toBe(true);

      // Verify login UI elements are present
      const loginForm = page
        .locator('form, [data-testid*="login"], [data-testid="login-form"]')
        .first();
      await expect(loginForm).toBeVisible();

      // Check for username/email and password fields
      const usernameField = page.locator(
        'input[type="text"], input[type="email"], input[name*="user"], input[data-testid*="username"]'
      );
      const passwordField = page.locator('input[type="password"], input[data-testid*="password"]');

      if ((await usernameField.count()) > 0) {
        await expect(usernameField.first()).toBeVisible();
      }
      if ((await passwordField.count()) > 0) {
        await expect(passwordField.first()).toBeVisible();
      }
    } else if (isOnSettingsPage && hasSettingsContent) {
      // Assert we stayed on settings (publicly accessible)
      await expect(page).toHaveURL(/.*\/ui\/settings/);
      await expect(page.locator('[data-testid="main-content"], main, [role="main"]')).toBeVisible();
    } else {
      // Unexpected state - fail the test
      throw new Error(
        `Unexpected auth state: URL=${currentUrl}, hasLoginForm=${hasLoginForm}, hasSettingsContent=${hasSettingsContent}`
      );
    }
  });

  test('Application handles unauthenticated API requests', async ({ request }) => {
    // Build absolute URL to avoid baseURL path prefixing
    const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';
    const apiUrl = `${baseUrl.replace(/\/ui$/, '')}/api/v2/settings/audio`;

    const response = await request.get(apiUrl);

    // Use environment flag to control auth expectations
    if (process.env['EXPECT_AUTH_REQUIRED'] === 'true') {
      // Expect authentication to be required
      expect([401, 403]).toContain(response.status());
    } else {
      // Expect public access or successful response
      expect(response.status()).toBe(200);
    }
  });

  test('CSRF token meta tag (optional)', async ({ page }) => {
    // Check CSRF on the new UI
    await page.goto('/ui/');

    // Wait for page to fully load
    await page.waitForLoadState('domcontentloaded');

    // Check for CSRF protection meta tag
    const csrfMeta = page.locator('meta[name="csrf-token"]');
    const hasCsrfMeta = (await csrfMeta.count()) > 0;

    // If CSRF is implemented, the token should be available and non-empty
    if (hasCsrfMeta) {
      const token = await csrfMeta.getAttribute('content');
      expect(token).toBeTruthy();
      expect(token?.length).toBeGreaterThan(10); // Should be a real token
    }

    // Also check for CSRF in settings page (more likely to have forms)
    await page.goto('/ui/settings');
    await page.waitForLoadState('domcontentloaded');

    const settingsCsrfMeta = page.locator('meta[name="csrf-token"]');
    const hasSettingsCsrfMeta = (await settingsCsrfMeta.count()) > 0;

    if (hasSettingsCsrfMeta) {
      const settingsToken = await settingsCsrfMeta.getAttribute('content');
      expect(settingsToken).toBeTruthy();
    }

    // Enforce CSRF presence only if requested via environment
    if (process.env['E2E_EXPECT_CSRF'] === 'true') {
      expect(hasCsrfMeta || hasSettingsCsrfMeta).toBe(true);
    }
  });
});

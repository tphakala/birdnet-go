import { test, expect, type APIRequestContext } from '@playwright/test';

test.describe('Notification Delete Modal', () => {
  // Set reasonable timeout for each test
  test.setTimeout(30000);

  // Helper to create a test notification via API with timeout
  async function createTestNotification(request: APIRequestContext): Promise<string | null> {
    const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';

    try {
      // Get CSRF token from app config with timeout
      const configResponse = await request.get(`${baseUrl}/api/v2/app/config`, {
        timeout: 5000,
      });

      if (!configResponse.ok()) {
        return null;
      }

      const config = await configResponse.json();
      const csrfToken = config.csrfToken ?? '';

      // Create test notification with timeout
      const response = await request.post(`${baseUrl}/api/v2/notifications/test/new-species`, {
        headers: {
          'X-CSRF-Token': csrfToken,
        },
        timeout: 5000,
      });

      if (!response.ok()) {
        return null;
      }

      const notification = await response.json();
      return notification.id as string;
    } catch {
      return null;
    }
  }

  // Helper to delete notification via API (cleanup) - silent failures ok
  async function deleteNotificationAPI(request: APIRequestContext, id: string | null) {
    if (!id) return;

    const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';

    try {
      const configResponse = await request.get(`${baseUrl}/api/v2/app/config`, {
        timeout: 5000,
      });
      if (!configResponse.ok()) return;

      const config = await configResponse.json();
      const csrfToken = config.csrfToken ?? '';

      await request.delete(`${baseUrl}/api/v2/notifications/${id}`, {
        headers: {
          'X-CSRF-Token': csrfToken,
        },
        timeout: 5000,
      });
    } catch {
      // Cleanup failures are ok - notification may already be deleted
    }
  }

  // Helper to switch to flat view and find delete button
  async function switchToFlatViewAndGetDeleteButton(page: import('@playwright/test').Page) {
    // Wait for the page to load
    await expect(page.locator('text=New Species')).toBeVisible({ timeout: 10000 });

    // Switch to Flat view to see individual notifications with delete buttons
    const flatViewTab = page.locator('button:has-text("Flat view"), [role="tab"]:has-text("Flat")');
    await expect(flatViewTab).toBeVisible({ timeout: 5000 });
    await flatViewTab.click();

    // Wait for view to switch - in flat view we should see individual notifications
    // The delete button should now be visible
    const deleteButton = page
      .locator(
        'button[aria-label*="delete" i], button[aria-label*="Delete" i], button[aria-label*="supprimer" i]'
      )
      .first();

    // Wait for delete button to be visible
    await expect(deleteButton).toBeVisible({ timeout: 5000 });

    return deleteButton;
  }

  test('delete modal appears when clicking trashcan icon', async ({ page, request }) => {
    // Create a test notification
    const notificationId = await createTestNotification(request);
    // eslint-disable-next-line playwright/no-skipped-test -- Conditional skip when API preconditions fail
    test.skip(!notificationId, 'Could not create test notification - skipping test');

    try {
      // Navigate to notifications page
      await page.goto('/ui/notifications', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Switch to flat view and get delete button
      const deleteButton = await switchToFlatViewAndGetDeleteButton(page);

      // Click the delete button
      await deleteButton.click();

      // Verify the modal appears - dialog element with open attribute
      const modal = page.locator('dialog[open]');
      await expect(modal).toBeVisible({ timeout: 5000 });

      // Verify modal content - should have heading and buttons
      await expect(modal.locator('h3')).toBeVisible();
      await expect(
        modal.locator('button:has-text("Cancel"), button:has-text("Annuler")')
      ).toBeVisible();
      await expect(
        modal.locator('button:has-text("Delete"), button:has-text("Supprimer")')
      ).toBeVisible();
    } finally {
      // Cleanup: delete the notification
      await deleteNotificationAPI(request, notificationId);
    }
  });

  test('cancel button closes modal without deleting', async ({ page, request }) => {
    const notificationId = await createTestNotification(request);
    // eslint-disable-next-line playwright/no-skipped-test -- Conditional skip when API preconditions fail
    test.skip(!notificationId, 'Could not create test notification - skipping test');

    try {
      await page.goto('/ui/notifications', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Switch to flat view and click delete
      const deleteButton = await switchToFlatViewAndGetDeleteButton(page);
      await deleteButton.click();

      // Modal should be visible
      const modal = page.locator('dialog[open]');
      await expect(modal).toBeVisible({ timeout: 5000 });

      // Click cancel
      const cancelButton = modal.locator('button:has-text("Cancel"), button:has-text("Annuler")');
      await cancelButton.click();

      // Modal should close
      await expect(modal).not.toBeVisible({ timeout: 5000 });

      // Notification should still exist
      await expect(page.locator('text=New Species')).toBeVisible();
    } finally {
      await deleteNotificationAPI(request, notificationId);
    }
  });

  test('delete button removes notification', async ({ page, request }) => {
    const notificationId = await createTestNotification(request);
    // eslint-disable-next-line playwright/no-skipped-test -- Conditional skip when API preconditions fail
    test.skip(!notificationId, 'Could not create test notification - skipping test');

    await page.goto('/ui/notifications', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

    // Switch to flat view and get the delete button
    const deleteButton = await switchToFlatViewAndGetDeleteButton(page);

    // Get a reference to the parent card before clicking delete
    const notificationCard = deleteButton.locator('xpath=ancestor::div[contains(@class, "card")]');
    await expect(notificationCard).toBeVisible();

    await deleteButton.click();

    // Modal should be visible
    const modal = page.locator('dialog[open]');
    await expect(modal).toBeVisible({ timeout: 5000 });

    // Click delete in modal
    const confirmDeleteButton = modal.locator(
      'button:has-text("Delete"), button:has-text("Supprimer")'
    );
    await confirmDeleteButton.click();

    // Modal should close
    await expect(modal).not.toBeVisible({ timeout: 5000 });

    // The specific notification card should be removed from the UI
    await expect(notificationCard).not.toBeVisible({ timeout: 5000 });
  });

  test('modal has proper backdrop styling', async ({ page, request }) => {
    const notificationId = await createTestNotification(request);
    // eslint-disable-next-line playwright/no-skipped-test -- Conditional skip when API preconditions fail
    test.skip(!notificationId, 'Could not create test notification - skipping test');

    try {
      await page.goto('/ui/notifications', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Switch to flat view and click delete
      const deleteButton = await switchToFlatViewAndGetDeleteButton(page);
      await deleteButton.click();

      // Verify modal is visible and properly styled
      const modal = page.locator('dialog[open]');
      await expect(modal).toBeVisible({ timeout: 5000 });

      // Check modal has expected structure
      await expect(modal.locator('div').first()).toBeVisible();

      // The dialog should be centered (check it has positioning classes)
      const dialogClasses = await modal.getAttribute('class');
      expect(dialogClasses).toContain('fixed');

      // Close modal
      const cancelButton = modal.locator('button:has-text("Cancel"), button:has-text("Annuler")');
      await cancelButton.click();
      await expect(modal).not.toBeVisible({ timeout: 5000 });
    } finally {
      await deleteNotificationAPI(request, notificationId);
    }
  });

  test('escape key closes modal', async ({ page, request }) => {
    const notificationId = await createTestNotification(request);
    // eslint-disable-next-line playwright/no-skipped-test -- Conditional skip when API preconditions fail
    test.skip(!notificationId, 'Could not create test notification - skipping test');

    try {
      await page.goto('/ui/notifications', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded', { timeout: 10000 });

      // Switch to flat view and click delete
      const deleteButton = await switchToFlatViewAndGetDeleteButton(page);
      await deleteButton.click();

      const modal = page.locator('dialog[open]');
      await expect(modal).toBeVisible({ timeout: 5000 });

      // Press Escape to close (native dialog behavior)
      await page.keyboard.press('Escape');

      // Modal should close
      await expect(modal).not.toBeVisible({ timeout: 5000 });

      // Notification should still exist
      await expect(page.locator('text=New Species')).toBeVisible();
    } finally {
      await deleteNotificationAPI(request, notificationId);
    }
  });
});

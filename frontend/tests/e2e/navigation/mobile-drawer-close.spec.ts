import { test, expect, type Page } from '@playwright/test';

/**
 * Helper: open the mobile drawer by clicking the hamburger menu button.
 * Only visible on viewports narrower than lg (1024px).
 */
async function openDrawer(page: Page) {
  const hamburger = page
    .locator('button[aria-label]', {
      has: page.locator('svg'),
    })
    .filter({ has: page.locator('.lucide-menu') });

  // Fall back to the sidebar toggle button that is visible on mobile
  const toggle = hamburger
    .or(
      page
        .locator('button')
        .filter({ hasText: /toggle/i })
        .first()
    )
    .first();

  // If the hamburger is not visible, try the generic drawer toggle
  if (await toggle.isVisible()) {
    await toggle.click();
  } else {
    // Click the label for the drawer checkbox as fallback
    const drawerToggle = page.locator('label[for="my-drawer"]').first();
    if (await drawerToggle.isVisible()) {
      await drawerToggle.click();
    }
  }

  // Wait for the drawer to be visible
  await expect(page.locator('.drawer-side nav')).toBeVisible();
}

/**
 * Check whether the drawer checkbox is checked (drawer open).
 */
async function isDrawerOpen(page: Page): Promise<boolean> {
  return page.locator('#my-drawer').isChecked();
}

test.describe('Mobile drawer closes after navigation', () => {
  // Only meaningful on mobile viewports where the drawer is used
  test.beforeEach(async ({ page }, testInfo) => {
    const projectName = testInfo.project.name.toLowerCase();
    if (!projectName.includes('mobile')) {
      testInfo.skip(true, 'This test only runs on mobile viewport projects');
    }
    await page.goto('/ui/dashboard');
    await page.waitForLoadState('domcontentloaded');
  });

  test('drawer closes when tapping a top-level menu item', async ({ page }) => {
    // Open the drawer
    await openDrawer(page);
    expect(await isDrawerOpen(page)).toBe(true);

    // Click a navigation button in the sidebar (e.g. "Search" / magnifying glass)
    const searchButton = page
      .locator('.drawer-side nav button')
      .filter({
        has: page.locator('.lucide-search'),
      })
      .first();

    if (await searchButton.isVisible()) {
      await searchButton.click();
    } else {
      // Fallback: click any nav button that isn't the current page
      const navButtons = page.locator('.drawer-side nav button');
      const count = await navButtons.count();
      for (let i = 0; i < count; i++) {
        const btn = navButtons.nth(i);
        // Skip buttons that look like section expanders (chevrons)
        const hasChevron = await btn.locator('.lucide-chevron-down').count();
        if (hasChevron > 0) continue;
        if (await btn.isVisible()) {
          await btn.click();
          break;
        }
      }
    }

    // Drawer should be closed after navigation
    await expect(page.locator('#my-drawer')).not.toBeChecked();
  });

  test('drawer closes when tapping the overlay', async ({ page }) => {
    // Open the drawer
    await openDrawer(page);
    expect(await isDrawerOpen(page)).toBe(true);

    // Click the overlay (the label that covers the content area)
    const overlay = page.locator('.drawer-overlay').first();
    if (await overlay.isVisible()) {
      await overlay.click();
    }

    // Drawer should be closed
    await expect(page.locator('#my-drawer')).not.toBeChecked();
  });

  test('drawer closes when navigating to a sub-menu item', async ({ page }) => {
    // Open the drawer
    await openDrawer(page);
    expect(await isDrawerOpen(page)).toBe(true);

    // Try to expand the Settings section and click a sub-item
    const settingsSection = page
      .locator('.drawer-side nav button')
      .filter({
        has: page.locator('.lucide-settings'),
      })
      .first();

    await expect(settingsSection).toBeVisible();
    await settingsSection.click();

    // Look for a sub-menu item (e.g. "Main" settings) and wait for it to appear
    const subItem = page
      .locator('.drawer-side nav button')
      .filter({
        has: page.locator('.lucide-sliders-horizontal'),
      })
      .first();

    await expect(subItem).toBeVisible();
    await subItem.click();

    // Drawer should be closed after navigating to sub-item
    await expect(page.locator('#my-drawer')).not.toBeChecked();
  });
});

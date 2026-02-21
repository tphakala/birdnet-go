import { test, expect, type Page } from '@playwright/test';

test.describe('Alert Rules Settings Page', () => {
  test.setTimeout(30000);

  const baseUrl = process.env['BASE_URL'] ?? 'http://localhost:8080';

  /**
   * Helper: navigate to alert rules settings page and wait for content.
   */
  async function navigateToAlertRules(page: Page) {
    await page.goto('/ui/settings/alertrules', { timeout: 15000 });
    await page.waitForLoadState('domcontentloaded', { timeout: 10000 });
  }

  /**
   * Helper: wait for loading spinner to disappear if visible.
   */
  async function waitForSpinner(page: Page) {
    const spinner = page.locator('[role="status"] .animate-spin');
    if (await spinner.isVisible({ timeout: 2000 }).catch(() => false)) {
      await expect(spinner).not.toBeVisible({ timeout: 15000 });
    }
  }

  test.describe('Page Load', () => {
    test('alert rules settings page loads without errors', async ({ page }) => {
      // Collect console errors during page load
      const consoleErrors: string[] = [];
      page.on('console', msg => {
        if (msg.type() === 'error') {
          consoleErrors.push(msg.text());
        }
      });

      await navigateToAlertRules(page);

      // Should be on the correct URL
      await expect(page).toHaveURL(/.*\/ui\/settings\/alertrules/);

      // Should have main content area
      const mainContent = page.locator('main, [role="main"], [data-testid="main-content"]').first();
      await expect(mainContent).toBeVisible();

      // Should not have critical Svelte errors (like each_key_duplicate)
      const svelteErrors = consoleErrors.filter(
        e => e.includes('svelte.dev/e/') || e.includes('each_key_duplicate')
      );
      expect(svelteErrors, 'No Svelte runtime errors on page load').toHaveLength(0);
    });

    test('rules tab is active by default and shows content', async ({ page }) => {
      await navigateToAlertRules(page);

      // Rules tab should be selected
      const rulesTab = page.locator('#settings-tab-rules');
      await expect(rulesTab).toBeVisible({ timeout: 10000 });
      await expect(rulesTab).toHaveAttribute('aria-selected', 'true');

      // Rules tab panel should be visible
      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible();
    });

    test('rules list loads and displays rule cards', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      // Should display rule cards (default rules are seeded by the engine)
      // Each rule card is a div with rounded-lg inside the rules list
      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });

      // Should have at least one rule (defaults are seeded)
      const count = await ruleCards.count();
      expect(count, 'Should have at least one alert rule').toBeGreaterThan(0);
    });

    test('no duplicate key errors when rules load', async ({ page }) => {
      // This specifically tests the each_key_duplicate bug
      const errors: string[] = [];
      page.on('pageerror', err => {
        errors.push(err.message);
      });
      page.on('console', msg => {
        if (msg.type() === 'error') {
          errors.push(msg.text());
        }
      });

      await navigateToAlertRules(page);
      await waitForSpinner(page);

      // Wait for rule cards to render (deterministic wait instead of timeout)
      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });

      const duplicateKeyErrors = errors.filter(e => e.includes('each_key_duplicate'));
      expect(
        duplicateKeyErrors,
        'No each_key_duplicate errors — rules should have unique IDs'
      ).toHaveLength(0);
    });
  });

  test.describe('API Contract', () => {
    test('alert rules API returns valid response', async ({ request }) => {
      const resp = await request.get(`${baseUrl}/api/v2/alerts/rules`, { timeout: 10000 });
      expect(resp.ok(), `Rules API responded with status ${resp.status()}`).toBeTruthy();

      const data = await resp.json();
      expect(data).toHaveProperty('rules');
      expect(Array.isArray(data.rules)).toBe(true);
      expect(data).toHaveProperty('count');
      expect(typeof data.count).toBe('number');
    });

    test('alert rules have unique IDs', async ({ request }) => {
      const resp = await request.get(`${baseUrl}/api/v2/alerts/rules`, { timeout: 10000 });
      expect(resp.ok()).toBeTruthy();

      const data = await resp.json();
      const rules = data.rules as { id: number; name: string }[];

      // Check for duplicate IDs
      const ids = rules.map(r => r.id);
      const uniqueIds = new Set(ids);
      expect(
        uniqueIds.size,
        `Expected ${ids.length} unique IDs but found ${uniqueIds.size}. IDs: ${JSON.stringify(ids)}`
      ).toBe(ids.length);

      // Check for duplicate names (default rules should be unique by name)
      const names = rules.map(r => r.name);
      const uniqueNames = new Set(names);
      expect(
        uniqueNames.size,
        `Expected ${names.length} unique names but found ${uniqueNames.size}. Duplicates: ${JSON.stringify(names.filter((n, i) => names.indexOf(n) !== i))}`
      ).toBe(names.length);
    });

    test('alert schema API returns valid response', async ({ request }) => {
      const resp = await request.get(`${baseUrl}/api/v2/alerts/schema`, { timeout: 10000 });
      expect(resp.ok(), `Schema API responded with status ${resp.status()}`).toBeTruthy();

      const data = await resp.json();
      expect(data).toHaveProperty('objectTypes');
      expect(Array.isArray(data.objectTypes)).toBe(true);
      expect(data.objectTypes.length).toBeGreaterThan(0);

      // Each object type should have name and label
      for (const ot of data.objectTypes) {
        expect(ot).toHaveProperty('name');
        expect(ot).toHaveProperty('label');
      }

      expect(data).toHaveProperty('operators');
      expect(Array.isArray(data.operators)).toBe(true);
    });

    test('alert history API returns valid response', async ({ request }) => {
      const resp = await request.get(`${baseUrl}/api/v2/alerts/history?limit=10`, {
        timeout: 10000,
      });
      expect(resp.ok(), `History API responded with status ${resp.status()}`).toBeTruthy();

      const data = await resp.json();
      expect(data).toHaveProperty('history');
      expect(Array.isArray(data.history)).toBe(true);
      expect(data).toHaveProperty('total');
      expect(typeof data.total).toBe('number');
    });
  });

  test.describe('UI Elements', () => {
    test('filter dropdowns are visible', async ({ page }) => {
      await navigateToAlertRules(page);

      // Wait for rules tab content
      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });

      // Object type filter and status filter should be present
      // SelectDropdown renders as a button with role or within the filters area
      const filterArea = rulesPanel.locator('.flex.flex-wrap.items-center.gap-3').first();
      await expect(filterArea).toBeVisible();
    });

    test('action buttons are visible (export, import, reset, new rule)', async ({ page }) => {
      await navigateToAlertRules(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });

      // Check for action buttons by their text content
      await expect(rulesPanel.locator('button:has-text("Export")')).toBeVisible({ timeout: 5000 });
      await expect(rulesPanel.locator('button:has-text("Import")')).toBeVisible();
      await expect(rulesPanel.locator('button:has-text("Reset")')).toBeVisible();
    });

    test('history tab is accessible and loads', async ({ page }) => {
      await navigateToAlertRules(page);

      // Click the history tab
      const historyTab = page.locator('#settings-tab-history');
      await expect(historyTab).toBeVisible({ timeout: 10000 });
      await historyTab.click();

      // History tab should become selected
      await expect(historyTab).toHaveAttribute('aria-selected', 'true');

      // History panel should be visible
      const historyPanel = page.locator('#settings-tabpanel-history');
      await expect(historyPanel).toBeVisible();

      // Should show history count or empty state
      const hasHistoryContent =
        (await historyPanel
          .locator('text=/\\d+ total/i')
          .isVisible()
          .catch(() => false)) ||
        (await historyPanel
          .locator('text=/no.*history/i')
          .isVisible()
          .catch(() => false)) ||
        (await historyPanel
          .locator('.divide-y')
          .isVisible()
          .catch(() => false));
      expect(hasHistoryContent, 'History tab should show count, entries, or empty state').toBe(
        true
      );
    });
  });

  test.describe('Rule Card Interactions', () => {
    test('rule cards have action buttons (edit, toggle, test, delete)', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      // Get the first rule card
      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();
      if (!(await firstRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
        // eslint-disable-next-line playwright/no-skipped-test -- No rules to test
        test.skip(true, 'No rule cards available to test');
        return;
      }

      // Each card should have 4 action buttons
      const actionButtons = firstRuleCard.locator('button[aria-label]');
      const buttonCount = await actionButtons.count();
      expect(buttonCount, 'Rule card should have 4 action buttons').toBe(4);
    });

    test('toggle button changes rule enabled state', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();
      if (!(await firstRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
        // eslint-disable-next-line playwright/no-skipped-test -- No rules to test
        test.skip(true, 'No rule cards available to test');
        return;
      }

      // Find toggle button (2nd action button)
      const toggleButton = firstRuleCard
        .locator('button[aria-label*="able" i], button[aria-label*="activ" i]')
        .first();
      await expect(toggleButton).toBeVisible();

      // Check if the card initially has opacity (disabled) or not
      const wasEnabled = !(await firstRuleCard.evaluate(el => el.classList.contains('opacity-60')));

      // Click toggle
      await toggleButton.click();

      // Wait for the toggle to take effect — opacity class should change
      if (wasEnabled) {
        // Was enabled, should now be disabled (has opacity-60)
        await expect(firstRuleCard).toHaveClass(/opacity-60/, { timeout: 5000 });
      } else {
        // Was disabled, should now be enabled (no opacity-60)
        await expect(firstRuleCard).not.toHaveClass(/opacity-60/, { timeout: 5000 });
      }

      // Toggle back to restore original state and verify it restored
      await toggleButton.click();
      if (wasEnabled) {
        await expect(firstRuleCard).not.toHaveClass(/opacity-60/, { timeout: 5000 });
      } else {
        await expect(firstRuleCard).toHaveClass(/opacity-60/, { timeout: 5000 });
      }
    });
  });

  test.describe('Inline Editor', () => {
    test('new rule button opens inline editor', async ({ page }) => {
      await navigateToAlertRules(page);

      // Wait for rules panel
      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });
      await waitForSpinner(page);

      // Click "New Rule" button
      const newRuleButton = rulesPanel.locator('button:has-text("New")').first();
      await expect(newRuleButton).toBeVisible({ timeout: 5000 });
      await newRuleButton.click();

      // Inline editor should appear (it's a card with border-primary)
      const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Editor should have name input, save/cancel buttons
      await expect(editor.locator('input').first()).toBeVisible();
      await expect(editor.locator('button:has-text("Cancel")')).toBeVisible();
      await expect(editor.locator('button:has-text("Create")')).toBeVisible();

      // "New Rule" button should be hidden while editor is open
      await expect(newRuleButton).toBeHidden();
    });

    test('cancel button closes inline editor', async ({ page }) => {
      await navigateToAlertRules(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });
      await waitForSpinner(page);

      // Open editor
      const newRuleButton = rulesPanel.locator('button:has-text("New")').first();
      await expect(newRuleButton).toBeVisible({ timeout: 5000 });
      await newRuleButton.click();

      // Editor should be visible
      const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Click cancel
      await editor.locator('button:has-text("Cancel")').click();

      // Editor should close
      await expect(editor).not.toBeVisible({ timeout: 5000 });

      // "New Rule" button should reappear
      await expect(newRuleButton).toBeVisible();
    });
  });
});

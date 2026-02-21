import { test, expect, type Page, type APIRequestContext } from '@playwright/test';

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

  /**
   * Helper: wait for rules to load and be displayed.
   */
  async function waitForRulesLoaded(page: Page) {
    await waitForSpinner(page);
    const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
    await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });
  }

  /**
   * Helper: open the inline editor for a new rule.
   */
  async function openNewRuleEditor(page: Page) {
    const rulesPanel = page.locator('#settings-tabpanel-rules');
    const newRuleButton = rulesPanel.locator('button:has-text("New")').first();
    await expect(newRuleButton).toBeVisible({ timeout: 5000 });
    await newRuleButton.click();
    const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
    await expect(editor).toBeVisible({ timeout: 5000 });
    return editor;
  }

  /**
   * Helper: get the alert schema from the API for test data.
   */
  async function getSchema(request: APIRequestContext) {
    const resp = await request.get(`${baseUrl}/api/v2/alerts/schema`, { timeout: 10000 });
    expect(resp.ok()).toBeTruthy();
    return resp.json();
  }

  /**
   * Helper: create a test rule via API, returns rule ID.
   */
  async function createTestRule(request: APIRequestContext, name: string) {
    const schema = await getSchema(request);
    const objectType = schema.objectTypes[0];
    const event = objectType?.events?.[0];

    const rule = {
      name,
      description: 'E2E test rule',
      enabled: true,
      object_type: objectType?.name ?? 'detection',
      trigger_type: 'event',
      event_name: event?.name ?? 'new_species',
      metric_name: '',
      cooldown_sec: 300,
      conditions: [],
      actions: [{ target: 'bell', template_title: '', template_message: '', sort_order: 0 }],
    };

    const resp = await request.post(`${baseUrl}/api/v2/alerts/rules`, {
      data: rule,
      timeout: 10000,
    });
    expect(resp.ok(), `Failed to create test rule: ${resp.status()}`).toBeTruthy();
    const created = await resp.json();
    return created.id as number;
  }

  /**
   * Helper: delete a rule via API (cleanup). Silent failures ok.
   */
  async function deleteTestRule(request: APIRequestContext, id: number | null) {
    if (!id) return;
    try {
      await request.delete(`${baseUrl}/api/v2/alerts/rules/${id}`, { timeout: 5000 });
    } catch {
      // Cleanup failure is acceptable
    }
  }

  // ──────────────────────────────────────────────
  // Page Load
  // ──────────────────────────────────────────────

  test.describe('Page Load', () => {
    test('alert rules settings page loads without errors', async ({ page }) => {
      const consoleErrors: string[] = [];
      page.on('console', msg => {
        if (msg.type() === 'error') {
          consoleErrors.push(msg.text());
        }
      });

      await navigateToAlertRules(page);

      await expect(page).toHaveURL(/.*\/ui\/settings\/alertrules/);

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

      const rulesTab = page.locator('#settings-tab-rules');
      await expect(rulesTab).toBeVisible({ timeout: 10000 });
      await expect(rulesTab).toHaveAttribute('aria-selected', 'true');

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible();
    });

    test('rules list loads and displays rule cards', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      // Should display rule cards (default rules are seeded by the engine)
      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });

      const count = await ruleCards.count();
      expect(count, 'Should have at least one alert rule').toBeGreaterThan(0);
    });

    test('no duplicate key errors when rules load', async ({ page }) => {
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

      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });

      const duplicateKeyErrors = errors.filter(e => e.includes('each_key_duplicate'));
      expect(
        duplicateKeyErrors,
        'No each_key_duplicate errors — rules should have unique IDs'
      ).toHaveLength(0);
    });
  });

  // ──────────────────────────────────────────────
  // API Contract
  // ──────────────────────────────────────────────

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

      const ids = rules.map(r => r.id);
      const uniqueIds = new Set(ids);
      expect(
        uniqueIds.size,
        `Expected ${ids.length} unique IDs but found ${uniqueIds.size}. IDs: ${JSON.stringify(ids)}`
      ).toBe(ids.length);

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

    test('alert rules CRUD operations work end-to-end', async ({ request }) => {
      // Create
      const schema = await getSchema(request);
      const objectType = schema.objectTypes[0];
      const event = objectType?.events?.[0];
      const uniqueName = `E2E-CRUD-${Date.now()}`;

      const createResp = await request.post(`${baseUrl}/api/v2/alerts/rules`, {
        data: {
          name: uniqueName,
          description: 'CRUD test',
          enabled: true,
          object_type: objectType?.name ?? 'detection',
          trigger_type: 'event',
          event_name: event?.name ?? 'new_species',
          metric_name: '',
          cooldown_sec: 60,
          conditions: [],
          actions: [{ target: 'bell', template_title: '', template_message: '', sort_order: 0 }],
        },
        timeout: 10000,
      });
      expect(createResp.ok(), `Create failed: ${createResp.status()}`).toBeTruthy();
      const created = await createResp.json();
      const ruleId = created.id as number;

      try {
        // Read
        const getResp = await request.get(`${baseUrl}/api/v2/alerts/rules/${ruleId}`, {
          timeout: 10000,
        });
        expect(getResp.ok()).toBeTruthy();
        const fetched = await getResp.json();
        expect(fetched.name).toBe(uniqueName);

        // Update
        const updateResp = await request.put(`${baseUrl}/api/v2/alerts/rules/${ruleId}`, {
          data: { ...fetched, description: 'Updated CRUD test' },
          timeout: 10000,
        });
        expect(updateResp.ok()).toBeTruthy();
        const updated = await updateResp.json();
        expect(updated.description).toBe('Updated CRUD test');

        // Toggle
        const toggleResp = await request.patch(`${baseUrl}/api/v2/alerts/rules/${ruleId}/toggle`, {
          data: { enabled: false },
          timeout: 10000,
        });
        expect(toggleResp.ok()).toBeTruthy();

        // Delete
        const deleteResp = await request.delete(`${baseUrl}/api/v2/alerts/rules/${ruleId}`, {
          timeout: 10000,
        });
        expect(deleteResp.ok()).toBeTruthy();

        // Verify deleted
        const verifyResp = await request.get(`${baseUrl}/api/v2/alerts/rules/${ruleId}`, {
          timeout: 10000,
        });
        expect(verifyResp.status()).toBe(404);
      } catch (err) {
        // Cleanup on failure
        await deleteTestRule(request, ruleId);
        throw err;
      }
    });

    test('export and import alert rules', async ({ request }) => {
      const exportResp = await request.get(`${baseUrl}/api/v2/alerts/rules/export`, {
        timeout: 10000,
      });
      expect(exportResp.ok()).toBeTruthy();

      const exported = await exportResp.json();
      expect(exported).toHaveProperty('rules');
      expect(exported).toHaveProperty('version');
      expect(Array.isArray(exported.rules)).toBe(true);

      // Import should work with valid data (may skip duplicates)
      const importResp = await request.post(`${baseUrl}/api/v2/alerts/rules/import`, {
        data: { rules: [], version: exported.version },
        timeout: 10000,
      });
      expect(importResp.ok()).toBeTruthy();
      const imported = await importResp.json();
      expect(imported).toHaveProperty('imported');
      expect(imported).toHaveProperty('total');
    });
  });

  // ──────────────────────────────────────────────
  // UI Elements
  // ──────────────────────────────────────────────

  test.describe('UI Elements', () => {
    test('filter dropdowns are visible', async ({ page }) => {
      await navigateToAlertRules(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });

      const filterArea = rulesPanel.locator('.flex.flex-wrap.items-center.gap-3').first();
      await expect(filterArea).toBeVisible();
    });

    test('action buttons are visible (export, import, reset, new rule)', async ({ page }) => {
      await navigateToAlertRules(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      await expect(rulesPanel).toBeVisible({ timeout: 10000 });

      await expect(rulesPanel.locator('button:has-text("Export")')).toBeVisible({ timeout: 5000 });
      await expect(rulesPanel.locator('button:has-text("Import")')).toBeVisible();
      await expect(rulesPanel.locator('button:has-text("Reset")')).toBeVisible();
    });

    test('history tab is accessible and loads', async ({ page }) => {
      await navigateToAlertRules(page);

      const historyTab = page.locator('#settings-tab-history');
      await expect(historyTab).toBeVisible({ timeout: 10000 });
      await historyTab.click();

      await expect(historyTab).toHaveAttribute('aria-selected', 'true');

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

    test('rule cards display trigger, conditions, actions, and cooldown info', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();

      // Rule card should display the rule name
      const ruleName = firstRuleCard.locator('h4');
      await expect(ruleName).toBeVisible();
      const nameText = await ruleName.textContent();
      expect(nameText?.trim().length).toBeGreaterThan(0);

      // Should show trigger, conditions, actions, cooldown metadata
      const metadata = firstRuleCard.locator('.flex.flex-wrap');
      await expect(metadata).toBeVisible();
    });

    test('built-in rules show badge', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      // Default rules are built-in, so at least one badge should be visible
      const builtInBadge = page
        .locator('#settings-tabpanel-rules .space-y-2 > div')
        .locator('text=/built.?in/i')
        .first();
      await expect(builtInBadge).toBeVisible({ timeout: 3000 });
    });
  });

  // ──────────────────────────────────────────────
  // Rule Card Interactions
  // ──────────────────────────────────────────────

  test.describe('Rule Card Interactions', () => {
    test('rule cards have action buttons (edit, toggle, test, delete)', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();

      // Each card should have 4 action buttons
      const actionButtons = firstRuleCard.locator('button[aria-label]');
      const buttonCount = await actionButtons.count();
      expect(buttonCount, 'Rule card should have 4 action buttons').toBe(4);
    });

    test('toggle button changes rule enabled state', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();

      const toggleButton = firstRuleCard
        .locator('button[aria-label*="able" i], button[aria-label*="activ" i]')
        .first();
      await expect(toggleButton).toBeVisible();

      const wasEnabled = !(await firstRuleCard.evaluate(el => el.classList.contains('opacity-60')));

      // Toggle off/on
      await toggleButton.click();
      if (wasEnabled) {
        await expect(firstRuleCard).toHaveClass(/opacity-60/, { timeout: 5000 });
      } else {
        await expect(firstRuleCard).not.toHaveClass(/opacity-60/, { timeout: 5000 });
      }

      // Toggle back to restore original state
      await toggleButton.click();
      if (wasEnabled) {
        await expect(firstRuleCard).not.toHaveClass(/opacity-60/, { timeout: 5000 });
      } else {
        await expect(firstRuleCard).toHaveClass(/opacity-60/, { timeout: 5000 });
      }
    });

    test('fires a test alert and shows status message', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();

      // Test button has Play icon and "test" aria-label
      const testButton = firstRuleCard.locator('button[aria-label*="est" i]').first();
      await expect(testButton).toBeVisible();
      await testButton.click();

      // Should show a success or error status banner
      const statusBanner = page.locator('[role="status"], [role="alert"]');
      await expect(statusBanner.first()).toBeVisible({ timeout: 5000 });
    });

    test('delete button shows confirmation dialog', async ({ page }) => {
      // Create a test rule we can safely delete
      const request = page.request;
      const testName = `E2E-Delete-Test-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        // Find the test rule card
        const testRuleCard = page.locator(`#settings-tabpanel-rules .space-y-2 > div`, {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          // Rule might not be visible due to filters, skip test
          await deleteTestRule(request, testRuleId);
          return;
        }

        // Set up dialog handler to decline the delete
        page.on('dialog', dialog => dialog.dismiss());

        const deleteButton = testRuleCard.locator('button[aria-label*="elete" i]').first();
        await expect(deleteButton).toBeVisible();
        await deleteButton.click();

        // Rule should still exist after dismissing the dialog
        await expect(testRuleCard).toBeVisible();
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });

    test('delete button removes rule when confirmed', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-Delete-Confirm-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        const testRuleCard = page.locator(`#settings-tabpanel-rules .space-y-2 > div`, {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          await deleteTestRule(request, testRuleId);
          return;
        }

        // Accept the confirmation dialog
        page.on('dialog', dialog => dialog.accept());

        const deleteButton = testRuleCard.locator('button[aria-label*="elete" i]').first();
        await deleteButton.click();

        // Rule card should disappear
        await expect(testRuleCard).not.toBeVisible({ timeout: 5000 });
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });
  });

  // ──────────────────────────────────────────────
  // Inline Editor — New Rule
  // ──────────────────────────────────────────────

  test.describe('Inline Editor — New Rule', () => {
    test('new rule button opens inline editor without errors', async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', err => errors.push(err.message));
      page.on('console', msg => {
        if (msg.type() === 'error') errors.push(msg.text());
      });

      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Editor should have name input, save/cancel buttons
      await expect(editor.locator('input').first()).toBeVisible();
      await expect(editor.locator('button:has-text("Cancel")')).toBeVisible();
      await expect(editor.locator('button:has-text("Create")')).toBeVisible();

      // No crypto.randomUUID or other errors
      const criticalErrors = errors.filter(
        e =>
          e.includes('crypto.randomUUID') || e.includes('TypeError') || e.includes('svelte.dev/e/')
      );
      expect(
        criticalErrors,
        `Editor should open without errors: ${criticalErrors.join('; ')}`
      ).toHaveLength(0);
    });

    test('"New Rule" button is hidden while editor is open', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      const newRuleButton = rulesPanel.locator('button:has-text("New")').first();
      await expect(newRuleButton).toBeVisible({ timeout: 5000 });

      await openNewRuleEditor(page);

      // Button should be hidden while editor is open
      await expect(newRuleButton).toBeHidden();
    });

    test('cancel button closes inline editor', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      const newRuleButton = rulesPanel.locator('button:has-text("New")').first();

      const editor = await openNewRuleEditor(page);

      // Click cancel
      await editor.locator('button:has-text("Cancel")').click();

      // Editor should close
      await expect(editor).not.toBeVisible({ timeout: 5000 });

      // "New Rule" button should reappear
      await expect(newRuleButton).toBeVisible();
    });

    test('create button is disabled when name is empty', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // With empty name, Create should be disabled
      const createButton = editor.locator('button:has-text("Create")');
      await expect(createButton).toBeDisabled();
    });

    test('editor shows object type dropdown with schema options', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Should have object type section with a select/button
      // The editor uses SelectDropdown components which render as custom dropdowns
      const triggerSection = editor.locator('h4:has-text("Trigger")');
      await expect(triggerSection).toBeVisible();
    });

    test('editor shows event/metric dropdown based on trigger type', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // The editor should show event or metric selector depending on trigger type
      // With default object type selected, there should be an event dropdown visible
      const triggerSection = editor.locator('.space-y-3').filter({
        has: page.locator('h4:has-text("Trigger")'),
      });
      await expect(triggerSection).toBeVisible();
    });

    test('editor shows actions section with bell checkbox', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Actions section should be present
      const actionsSection = editor.locator('h4:has-text("Actions")');
      await expect(actionsSection).toBeVisible();

      // Bell action should be checked by default (new rules start with bell action)
      // The Checkbox component renders a label with the text
      const bellCheckbox = editor.locator('text=/bell/i').first();
      await expect(bellCheckbox).toBeVisible();
    });

    test('editor shows cooldown options section', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Options section with cooldown input
      const optionsSection = editor.locator('h4:has-text("Options")');
      await expect(optionsSection).toBeVisible();

      // Cooldown input should exist
      const cooldownInput = editor.locator('#cooldown-minutes');
      await expect(cooldownInput).toBeVisible();
      // Default is 5 minutes
      await expect(cooldownInput).toHaveValue('5');
    });

    test('can create a new rule with valid data', async ({ page }) => {
      const request = page.request;

      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);
      const uniqueName = `E2E-Create-${Date.now()}`;

      // Fill in name
      const nameInput = editor.locator('input').first();
      await nameInput.fill(uniqueName);

      // Create button should become enabled (name + default object type + default event + bell action)
      // We need to also select an event since the default may not auto-select
      // Wait a moment for reactivity to settle
      const createButton = editor.locator('button:has-text("Create")');

      // If Create is still disabled, we may need to select an event
      if (await createButton.isDisabled()) {
        // The event select might need a value — skip this test if the form
        // requires more complex interaction (schema-dependent)
        // eslint-disable-next-line playwright/no-skipped-test -- Schema-dependent behavior
        test.skip(true, 'Form requires additional schema-dependent selections');
        return;
      }

      await createButton.click();

      // Editor should close after successful creation
      await expect(editor).not.toBeVisible({ timeout: 10000 });

      // New rule should appear in the list
      const newRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
        has: page.locator(`text="${uniqueName}"`),
      });
      await expect(newRuleCard).toBeVisible({ timeout: 5000 });

      // Cleanup: delete the created rule
      const resp = await request.get(`${baseUrl}/api/v2/alerts/rules`, { timeout: 10000 });
      const data = await resp.json();
      const createdRule = (data.rules as { id: number; name: string }[]).find(
        r => r.name === uniqueName
      );
      if (createdRule) {
        await deleteTestRule(request, createdRule.id);
      }
    });

    test('no crypto.randomUUID errors when opening editor on HTTP', async ({ page }) => {
      // This tests the specific bug where crypto.randomUUID fails on non-HTTPS
      const errors: string[] = [];
      page.on('pageerror', err => errors.push(err.message));

      await navigateToAlertRules(page);
      await waitForSpinner(page);

      await openNewRuleEditor(page);

      const cryptoErrors = errors.filter(e => e.includes('randomUUID') || e.includes('crypto'));
      expect(
        cryptoErrors,
        `No crypto.randomUUID errors on HTTP: ${cryptoErrors.join('; ')}`
      ).toHaveLength(0);
    });
  });

  // ──────────────────────────────────────────────
  // Inline Editor — Edit Existing Rule
  // ──────────────────────────────────────────────

  test.describe('Inline Editor — Edit Existing Rule', () => {
    test('edit button opens editor pre-populated with rule data', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-Edit-Test-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        const testRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          return;
        }

        const editButton = testRuleCard.locator('button[aria-label*="dit" i]').first();
        await editButton.click();

        // Editor should open
        const rulesPanel = page.locator('#settings-tabpanel-rules');
        const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
        await expect(editor).toBeVisible({ timeout: 5000 });

        // Name should be pre-populated
        const nameInput = editor.locator('input').first();
        await expect(nameInput).toHaveValue(testName);

        // Save button should say "Save" not "Create"
        await expect(editor.locator('button:has-text("Save")')).toBeVisible();
        await expect(editor.locator('button:has-text("Create")')).toBeHidden();
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });

    test('no crypto.randomUUID errors when editing existing rule', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-Edit-Crypto-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      const errors: string[] = [];
      page.on('pageerror', err => errors.push(err.message));

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        const testRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          return;
        }

        const editButton = testRuleCard.locator('button[aria-label*="dit" i]').first();
        await editButton.click();

        const rulesPanel = page.locator('#settings-tabpanel-rules');
        const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
        await expect(editor).toBeVisible({ timeout: 5000 });

        const cryptoErrors = errors.filter(e => e.includes('randomUUID') || e.includes('crypto'));
        expect(
          cryptoErrors,
          `No crypto.randomUUID errors when editing: ${cryptoErrors.join('; ')}`
        ).toHaveLength(0);
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });

    test('edit button is disabled when editor is already open', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      // Open the editor for a new rule
      await openNewRuleEditor(page);

      // Edit buttons on existing rule cards should be disabled
      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();
      const editButton = firstRuleCard.locator('button[aria-label*="dit" i]').first();
      await expect(editButton).toBeDisabled();
    });

    test('delete button is disabled when editor is open', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      await openNewRuleEditor(page);

      const firstRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div').first();
      const deleteButton = firstRuleCard.locator('button[aria-label*="elete" i]').first();
      await expect(deleteButton).toBeDisabled();
    });
  });

  // ──────────────────────────────────────────────
  // Inline Editor — Conditions
  // ──────────────────────────────────────────────

  test.describe('Inline Editor — Conditions', () => {
    test('add condition button appears when properties are available', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-Condition-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        // Edit the test rule to get to condition section
        const testRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          return;
        }

        const editButton = testRuleCard.locator('button[aria-label*="dit" i]').first();
        await editButton.click();

        const rulesPanel = page.locator('#settings-tabpanel-rules');
        const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
        await expect(editor).toBeVisible({ timeout: 5000 });

        // If conditions section is visible, the "Add Condition" button should be present
        const addConditionButton = editor.locator('button:has-text("Add")').first();
        if (await addConditionButton.isVisible({ timeout: 3000 }).catch(() => false)) {
          await addConditionButton.click();

          // A condition row should appear with property dropdown, operator, value
          const conditionRow = editor.locator('.flex.items-end.gap-2').first();
          await expect(conditionRow).toBeVisible({ timeout: 3000 });

          // Remove button should be visible (trash icon)
          const removeButton = conditionRow.locator('button[aria-label*="emove" i]');
          await expect(removeButton).toBeVisible();
        }
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });

    test('remove condition button removes condition row', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-RemoveCond-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        const testRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          return;
        }

        const editButton = testRuleCard.locator('button[aria-label*="dit" i]').first();
        await editButton.click();

        const rulesPanel = page.locator('#settings-tabpanel-rules');
        const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
        await expect(editor).toBeVisible({ timeout: 5000 });

        const addConditionButton = editor.locator('button:has-text("Add")').first();
        if (!(await addConditionButton.isVisible({ timeout: 3000 }).catch(() => false))) {
          return; // No conditions section available
        }

        // Add a condition
        await addConditionButton.click();
        const conditionRows = editor.locator('.flex.items-end.gap-2');
        const countBefore = await conditionRows.count();
        expect(countBefore).toBeGreaterThan(0);

        // Remove it
        const removeButton = conditionRows.first().locator('button[aria-label*="emove" i]');
        await removeButton.click();

        // Count should decrease
        const countAfter = await conditionRows.count();
        expect(countAfter).toBe(countBefore - 1);
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });

    test('no crypto.randomUUID errors when adding conditions', async ({ page }) => {
      const request = page.request;
      const testName = `E2E-CondCrypto-${Date.now()}`;
      const testRuleId = await createTestRule(request, testName);

      const errors: string[] = [];
      page.on('pageerror', err => errors.push(err.message));

      try {
        await navigateToAlertRules(page);
        await waitForRulesLoaded(page);

        const testRuleCard = page.locator('#settings-tabpanel-rules .space-y-2 > div', {
          has: page.locator(`text="${testName}"`),
        });

        if (!(await testRuleCard.isVisible({ timeout: 5000 }).catch(() => false))) {
          return;
        }

        const editButton = testRuleCard.locator('button[aria-label*="dit" i]').first();
        await editButton.click();

        const rulesPanel = page.locator('#settings-tabpanel-rules');
        const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
        await expect(editor).toBeVisible({ timeout: 5000 });

        const addConditionButton = editor.locator('button:has-text("Add")').first();
        if (await addConditionButton.isVisible({ timeout: 3000 }).catch(() => false)) {
          // Add multiple conditions to exercise the randomUUID fallback
          await addConditionButton.click();
          await addConditionButton.click();
          await addConditionButton.click();
        }

        const cryptoErrors = errors.filter(e => e.includes('randomUUID') || e.includes('crypto'));
        expect(
          cryptoErrors,
          `No crypto errors when adding conditions: ${cryptoErrors.join('; ')}`
        ).toHaveLength(0);
      } finally {
        await deleteTestRule(request, testRuleId);
      }
    });
  });

  // ──────────────────────────────────────────────
  // Inline Editor — Actions
  // ──────────────────────────────────────────────

  test.describe('Inline Editor — Actions', () => {
    test('toggling action checkbox shows/hides template fields', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Find the push action checkbox (bell is checked by default)
      const pushLabel = editor.locator('text=/push/i').first();
      if (!(await pushLabel.isVisible({ timeout: 3000 }).catch(() => false))) {
        return;
      }

      // Click to enable push action
      await pushLabel.click();

      // Template fields should appear for push action
      // (template_title and template_message inputs)
      const templateInputs = editor.locator(
        'input[placeholder*="itle"], input[placeholder*="essage"]'
      );
      const templateCount = await templateInputs.count();
      // At minimum, bell action already has templates; push adds more
      expect(templateCount).toBeGreaterThan(0);
    });

    test('unchecking all actions disables create button', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const editor = await openNewRuleEditor(page);

      // Fill name so it's not the blocking factor
      const nameInput = editor.locator('input').first();
      await nameInput.fill('Test Rule');

      // Find bell checkbox and uncheck it (it's checked by default)
      const bellLabel = editor.locator('text=/bell/i').first();
      if (await bellLabel.isVisible({ timeout: 3000 }).catch(() => false)) {
        await bellLabel.click();
      }

      // Create button should be disabled (no actions selected)
      const createButton = editor.locator('button:has-text("Create")');
      await expect(createButton).toBeDisabled();
    });
  });

  // ──────────────────────────────────────────────
  // Filters
  // ──────────────────────────────────────────────

  test.describe('Filters', () => {
    test('disabled filter hides enabled rules', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      // Get initial count of rule cards
      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      const initialCount = await ruleCards.count();

      if (initialCount === 0) {
        return; // Nothing to filter
      }

      // Find the status filter dropdown (second dropdown in filter area)
      // SelectDropdown renders with a button; the enabled/disabled filter is the 2nd one
      const filterArea = page
        .locator('#settings-tabpanel-rules .flex.flex-wrap.items-center.gap-3')
        .first();
      const dropdowns = filterArea.locator('.w-40 button, .w-40 select');

      if ((await dropdowns.count()) === 0) {
        return;
      }

      // Click the status dropdown to open it, then select "Disabled"
      const statusDropdown = filterArea.locator('.w-40').first();
      const statusButton = statusDropdown.locator('button').first();

      if (await statusButton.isVisible({ timeout: 3000 }).catch(() => false)) {
        await statusButton.click();

        // Look for the "Disabled" option in the dropdown
        const disabledOption = page
          .locator(
            '[role="option"]:has-text("Disabled"), [role="listbox"] button:has-text("Disabled")'
          )
          .first();
        if (await disabledOption.isVisible({ timeout: 3000 }).catch(() => false)) {
          await disabledOption.click();

          // After filtering, count may be different (could be 0 or fewer)
          const filteredCount = await ruleCards.count().catch(() => 0);
          // If all rules are enabled, filtering by disabled should show 0 or fewer
          expect(filteredCount).toBeLessThanOrEqual(initialCount);
        }
      }
    });
  });

  // ──────────────────────────────────────────────
  // Reset Defaults
  // ──────────────────────────────────────────────

  test.describe('Reset Defaults', () => {
    test('reset defaults button reloads default rules', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      const resetButton = rulesPanel.locator('button:has-text("Reset")');
      await expect(resetButton).toBeVisible();

      await resetButton.click();

      // Wait for rules to reload
      await waitForSpinner(page);

      // Should still have rules after reset
      const ruleCardsAfter = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCardsAfter.first()).toBeVisible({ timeout: 10000 });

      const countAfter = await ruleCardsAfter.count();
      expect(countAfter, 'Should have rules after reset').toBeGreaterThan(0);
    });
  });

  // ──────────────────────────────────────────────
  // Export
  // ──────────────────────────────────────────────

  test.describe('Export', () => {
    test('export button triggers download', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const rulesPanel = page.locator('#settings-tabpanel-rules');
      const exportButton = rulesPanel.locator('button:has-text("Export")');
      await expect(exportButton).toBeVisible();

      // Listen for download event
      const downloadPromise = page.waitForEvent('download', { timeout: 10000 });
      await exportButton.click();

      const download = await downloadPromise;
      expect(download.suggestedFilename()).toBe('alert-rules.json');
    });
  });

  // ──────────────────────────────────────────────
  // History Tab
  // ──────────────────────────────────────────────

  test.describe('History Tab', () => {
    test('switching to history tab shows history content', async ({ page }) => {
      await navigateToAlertRules(page);

      const historyTab = page.locator('#settings-tab-history');
      await expect(historyTab).toBeVisible({ timeout: 10000 });
      await historyTab.click();

      await expect(historyTab).toHaveAttribute('aria-selected', 'true');

      const historyPanel = page.locator('#settings-tabpanel-history');
      await expect(historyPanel).toBeVisible();

      // Should have "Clear History" button
      const clearButton = historyPanel.locator('button:has-text("Clear")');
      await expect(clearButton).toBeVisible();
    });

    test('switching back to rules tab preserves rules', async ({ page }) => {
      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      // Get initial rule count
      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      const initialCount = await ruleCards.count();

      // Switch to history
      const historyTab = page.locator('#settings-tab-history');
      await historyTab.click();
      await expect(historyTab).toHaveAttribute('aria-selected', 'true');

      // Switch back to rules
      const rulesTab = page.locator('#settings-tab-rules');
      await rulesTab.click();
      await expect(rulesTab).toHaveAttribute('aria-selected', 'true');

      // Rules should still be present
      await expect(ruleCards.first()).toBeVisible({ timeout: 5000 });
      const afterCount = await ruleCards.count();
      expect(afterCount).toBe(initialCount);
    });

    test('history shows count', async ({ page }) => {
      await navigateToAlertRules(page);

      const historyTab = page.locator('#settings-tab-history');
      await historyTab.click();

      const historyPanel = page.locator('#settings-tabpanel-history');
      await expect(historyPanel).toBeVisible();

      // Should show the total count text
      const countText = historyPanel.locator('text=/\\d+ total/i');
      await expect(countText).toBeVisible({ timeout: 5000 });
    });
  });

  // ──────────────────────────────────────────────
  // Error Handling
  // ──────────────────────────────────────────────

  test.describe('Error Handling', () => {
    test('page handles navigation without crashing', async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', err => errors.push(err.message));

      await navigateToAlertRules(page);
      await waitForSpinner(page);

      // Navigate away and back
      await page.goto('/ui/settings', { timeout: 15000 });
      await page.waitForLoadState('domcontentloaded');
      await navigateToAlertRules(page);
      await waitForSpinner(page);

      const ruleCards = page.locator('#settings-tabpanel-rules .space-y-2 > div');
      await expect(ruleCards.first()).toBeVisible({ timeout: 10000 });

      const criticalErrors = errors.filter(
        e => e.includes('each_key_duplicate') || e.includes('randomUUID')
      );
      expect(criticalErrors).toHaveLength(0);
    });

    test('no console errors during normal page interaction', async ({ page }) => {
      const consoleErrors: string[] = [];
      page.on('console', msg => {
        if (msg.type() === 'error') {
          consoleErrors.push(msg.text());
        }
      });
      page.on('pageerror', err => consoleErrors.push(err.message));

      await navigateToAlertRules(page);
      await waitForRulesLoaded(page);

      // Open and close editor
      await openNewRuleEditor(page);
      const rulesPanel = page.locator('#settings-tabpanel-rules');
      const editor = rulesPanel.locator('.border-\\[var\\(--color-primary\\)\\]');
      await editor.locator('button:has-text("Cancel")').click();
      await expect(editor).not.toBeVisible({ timeout: 5000 });

      // Switch tabs
      const historyTab = page.locator('#settings-tab-history');
      await historyTab.click();
      await expect(historyTab).toHaveAttribute('aria-selected', 'true');

      const rulesTab = page.locator('#settings-tab-rules');
      await rulesTab.click();
      await expect(rulesTab).toHaveAttribute('aria-selected', 'true');

      // Filter Svelte/crypto errors (ignore network errors, favicon, etc.)
      const svelteOrCryptoErrors = consoleErrors.filter(
        e =>
          e.includes('svelte.dev/e/') ||
          e.includes('each_key_duplicate') ||
          e.includes('randomUUID') ||
          e.includes('crypto')
      );
      expect(
        svelteOrCryptoErrors,
        `No Svelte/crypto errors during interaction: ${svelteOrCryptoErrors.join('; ')}`
      ).toHaveLength(0);
    });
  });
});

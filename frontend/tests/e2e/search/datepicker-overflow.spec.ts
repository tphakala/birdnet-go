import { test, expect, type Page } from '@playwright/test';

/**
 * DatePicker Overflow & Visibility E2E Tests
 *
 * Regression tests for GitHub #2513: DatePicker calendar must not be clipped
 * by parent containers with overflow:hidden. The calendar uses position:fixed
 * to escape clipping contexts and should always be fully visible within the
 * viewport when opened.
 *
 * Also includes functional tests for date selection on the Search page.
 */

const SEARCH_URL = '/ui/search';
const NAVIGATION_TIMEOUT_MS = 15000;
const CONTENT_TIMEOUT_MS = 10000;
const INTERACTION_TIMEOUT_MS = 5000;
const SEARCH_FORM_SELECTOR = '#searchForm';
const DATEPICKER_TRIGGER_SELECTOR = '.datepicker-trigger';
const DATEPICKER_CALENDAR_SELECTOR = '.datepicker-calendar';
const DATEPICKER_WRAPPER_SELECTOR = '.datepicker-wrapper';

/** Navigate to the Search page and wait for it to load. */
const navigateToSearch = async (page: Page) => {
  await page.goto(SEARCH_URL, { timeout: NAVIGATION_TIMEOUT_MS });
  await page.waitForLoadState('domcontentloaded', { timeout: CONTENT_TIMEOUT_MS });

  // Wait for the search form to be present
  await expect(page.locator(SEARCH_FORM_SELECTOR)).toBeVisible({
    timeout: CONTENT_TIMEOUT_MS,
  });
};

/** Open the first DatePicker calendar (the "From" date picker). */
const openStartDatePicker = async (page: Page) => {
  const trigger = page.locator(DATEPICKER_TRIGGER_SELECTOR).first();
  await expect(trigger).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
  await trigger.click();

  // Wait for the calendar dialog to appear
  const calendar = page.locator(DATEPICKER_CALENDAR_SELECTOR);
  await expect(calendar).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });

  return calendar;
};

/** Open the second DatePicker calendar (the "To" date picker). */
const openEndDatePicker = async (page: Page) => {
  const trigger = page.locator(DATEPICKER_TRIGGER_SELECTOR).nth(1);
  await expect(trigger).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
  await trigger.click();

  // Wait for the calendar dialog to appear
  const calendar = page.locator(DATEPICKER_CALENDAR_SELECTOR);
  await expect(calendar).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });

  return calendar;
};

test.describe('Search DatePicker - Overflow and Visibility', () => {
  test.setTimeout(30000);

  test('start date calendar is fully visible within viewport when opened', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Get the calendar's bounding box and viewport size
    const calendarBox = await calendar.boundingBox();
    const viewportSize = page.viewportSize();

    expect(calendarBox).not.toBeNull();
    expect(viewportSize).not.toBeNull();

    if (calendarBox && viewportSize) {
      // Calendar should be within viewport bounds (not clipped off-screen)
      expect(
        calendarBox.x,
        'Calendar left edge should not extend past viewport left'
      ).toBeGreaterThanOrEqual(0);
      expect(
        calendarBox.y,
        'Calendar top edge should not extend past viewport top'
      ).toBeGreaterThanOrEqual(0);
      expect(
        calendarBox.x + calendarBox.width,
        'Calendar right edge should not extend past viewport right'
      ).toBeLessThanOrEqual(viewportSize.width);
      expect(
        calendarBox.y + calendarBox.height,
        'Calendar bottom edge should not extend past viewport bottom'
      ).toBeLessThanOrEqual(viewportSize.height);
    }
  });

  test('end date calendar is fully visible within viewport when opened', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openEndDatePicker(page);

    const calendarBox = await calendar.boundingBox();
    const viewportSize = page.viewportSize();

    expect(calendarBox).not.toBeNull();
    expect(viewportSize).not.toBeNull();

    if (calendarBox && viewportSize) {
      expect(
        calendarBox.x,
        'Calendar left edge should not extend past viewport left'
      ).toBeGreaterThanOrEqual(0);
      expect(
        calendarBox.y,
        'Calendar top edge should not extend past viewport top'
      ).toBeGreaterThanOrEqual(0);
      expect(
        calendarBox.x + calendarBox.width,
        'Calendar right edge should not extend past viewport right'
      ).toBeLessThanOrEqual(viewportSize.width);
      expect(
        calendarBox.y + calendarBox.height,
        'Calendar bottom edge should not extend past viewport bottom'
      ).toBeLessThanOrEqual(viewportSize.height);
    }
  });

  test('calendar uses position:fixed to escape overflow clipping contexts', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Verify the calendar is rendered with position:fixed (the fix for #2513)
    const position = await calendar.evaluate(el => {
      return window.getComputedStyle(el).position;
    });
    expect(position).toBe('fixed');

    // Verify the z-index is high enough to stack above other content
    const zIndex = await calendar.evaluate(el => {
      return window.getComputedStyle(el).zIndex;
    });
    expect(Number(zIndex)).toBeGreaterThanOrEqual(1100);
  });

  test('calendar is not clipped by parent overflow:hidden containers', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // The calendar should be actually visible (not hidden by overflow clipping)
    await expect(calendar).toBeVisible();

    // Verify the calendar has reasonable dimensions (not zero-sized from clipping)
    const calendarBox = await calendar.boundingBox();
    expect(calendarBox).not.toBeNull();

    if (calendarBox) {
      // Calendar should have a minimum width (the component sets min-width: 280px)
      expect(calendarBox.width, 'Calendar should have reasonable width').toBeGreaterThanOrEqual(
        280
      );
      // Calendar should have meaningful height (header + weekdays + grid + today button)
      expect(calendarBox.height, 'Calendar should have reasonable height').toBeGreaterThan(200);
    }
  });

  test('calendar renders all expected interactive elements', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Verify structural elements are present
    // Month/year heading
    const heading = calendar.locator('#month-year-heading');
    await expect(heading).toBeVisible();

    // Previous/next month navigation buttons
    const prevButton = calendar
      .locator('button')
      .filter({ has: page.locator('.lucide-chevron-left') });
    const nextButton = calendar
      .locator('button')
      .filter({ has: page.locator('.lucide-chevron-right') });
    await expect(prevButton).toBeVisible();
    await expect(nextButton).toBeVisible();

    // Calendar grid with day buttons
    const grid = calendar.locator('[role="grid"]');
    await expect(grid).toBeVisible();

    // Should have at least 28 day buttons (shortest month)
    const dayButtons = grid.locator('button[role="gridcell"]');
    const dayCount = await dayButtons.count();
    expect(dayCount).toBeGreaterThanOrEqual(28);

    // Today button at the bottom
    const todayButton = calendar.locator('.datepicker-today-btn');
    await expect(todayButton).toBeVisible();
  });
});

test.describe('Search DatePicker - Functional Tests', () => {
  test.setTimeout(30000);

  test('clicking a date selects it and closes the calendar', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Record the initial trigger text before selecting a date
    const trigger = page.locator(DATEPICKER_TRIGGER_SELECTOR).first();
    const initialText = (await trigger.textContent())?.trim();

    // Click on a selectable day (pick the 15th if available, or any enabled day)
    const selectableDay = calendar
      .locator(
        'button[role="gridcell"]:not(.datepicker-day-disabled):not(.datepicker-day-selected)'
      )
      .first();
    await expect(selectableDay).toBeVisible();
    await selectableDay.click();

    // Calendar should close after selection
    await expect(calendar).not.toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });

    // The trigger button text should have changed from the placeholder
    const updatedText = (await trigger.textContent())?.trim();
    expect(updatedText).toBeTruthy();
    expect(updatedText).not.toBe(initialText);
  });

  test('Today button selects today and closes calendar', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    const todayButton = calendar.locator('.datepicker-today-btn');
    await expect(todayButton).toBeVisible();

    // Only click if the Today button is enabled (today might exceed maxDate in some edge cases)
    const isDisabled = await todayButton.isDisabled();
    if (!isDisabled) {
      await todayButton.click();

      // Calendar should close
      await expect(calendar).not.toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
    }
  });

  test('Escape key closes the calendar', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Press Escape
    await page.keyboard.press('Escape');

    // Calendar should close
    await expect(calendar).not.toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
  });

  test('clicking outside closes the calendar', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    // Click on the page heading (outside the calendar)
    const heading = page.locator('#search-filters-heading');
    await heading.click();

    // Calendar should close
    await expect(calendar).not.toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
  });

  test('month navigation works without breaking layout', async ({ page }) => {
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);
    const viewportSize = page.viewportSize();

    // Get initial month text
    const heading = calendar.locator('#month-year-heading');
    const initialMonth = await heading.innerText();

    // Navigate to previous month
    const prevButton = calendar
      .locator('button')
      .filter({ has: page.locator('.lucide-chevron-left') });
    await prevButton.click();

    // Month should have changed
    await expect(heading).not.toHaveText(initialMonth);

    // Calendar should still be within viewport after month navigation
    const calendarBox = await calendar.boundingBox();
    expect(calendarBox).not.toBeNull();

    if (calendarBox && viewportSize) {
      expect(calendarBox.x + calendarBox.width).toBeLessThanOrEqual(viewportSize.width);
      expect(calendarBox.y + calendarBox.height).toBeLessThanOrEqual(viewportSize.height);
    }
  });
});

test.describe('Search DatePicker - Overflow on Different Viewport Sizes', () => {
  test.setTimeout(30000);

  test('calendar stays within viewport on narrow screens', async ({ page }) => {
    // Set a narrower viewport to test responsive behavior
    await page.setViewportSize({ width: 768, height: 1024 });
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    const calendarBox = await calendar.boundingBox();
    const viewportSize = page.viewportSize();

    expect(calendarBox).not.toBeNull();
    expect(viewportSize).not.toBeNull();

    if (calendarBox && viewportSize) {
      expect(
        calendarBox.x,
        'Calendar should not overflow left on narrow viewport'
      ).toBeGreaterThanOrEqual(0);
      expect(
        calendarBox.x + calendarBox.width,
        'Calendar should not overflow right on narrow viewport'
      ).toBeLessThanOrEqual(viewportSize.width);
      expect(
        calendarBox.y + calendarBox.height,
        'Calendar should not overflow bottom on narrow viewport'
      ).toBeLessThanOrEqual(viewportSize.height);
    }
  });

  test('calendar stays within viewport on tablet size', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 768 });
    await navigateToSearch(page);
    const calendar = await openStartDatePicker(page);

    const calendarBox = await calendar.boundingBox();
    const viewportSize = page.viewportSize();

    expect(calendarBox).not.toBeNull();
    expect(viewportSize).not.toBeNull();

    if (calendarBox && viewportSize) {
      expect(calendarBox.x).toBeGreaterThanOrEqual(0);
      expect(calendarBox.y).toBeGreaterThanOrEqual(0);
      expect(calendarBox.x + calendarBox.width).toBeLessThanOrEqual(viewportSize.width);
      expect(calendarBox.y + calendarBox.height).toBeLessThanOrEqual(viewportSize.height);
    }
  });
});

test.describe('UI Overflow - General Interactive Elements', () => {
  test.setTimeout(30000);

  test('card container does not clip datepicker calendar with overflow:hidden', async ({
    page,
  }) => {
    await navigateToSearch(page);

    // Verify the card container does not use overflow:hidden which would
    // clip the position:fixed calendar dropdown
    const cardOverflow = await page.evaluate(() => {
      const card = document.querySelector('.card');
      if (!card) return 'no-card-found';
      return window.getComputedStyle(card).overflow;
    });

    if (cardOverflow !== 'no-card-found') {
      expect(
        cardOverflow,
        'Card container should not use overflow:hidden to avoid clipping fixed-position dropdowns'
      ).not.toBe('hidden');
    }
  });

  test('datepicker calendar has correct ARIA dialog role', async ({ page }) => {
    await navigateToSearch(page);
    await openStartDatePicker(page);

    const calendar = page.locator(DATEPICKER_CALENDAR_SELECTOR);
    await expect(calendar).toHaveAttribute('role', 'dialog');

    // Should have an accessible label
    await expect(calendar).toHaveAttribute('aria-label', /.+/);
  });

  test('opening one datepicker and then the other closes the first', async ({ page }) => {
    await navigateToSearch(page);

    // Scope locators to each picker wrapper for precise assertions
    const startPicker = page.locator(DATEPICKER_WRAPPER_SELECTOR).first();
    const endPicker = page.locator(DATEPICKER_WRAPPER_SELECTOR).nth(1);

    // Open the start date picker
    const startTrigger = startPicker.locator(DATEPICKER_TRIGGER_SELECTOR);
    await startTrigger.click();
    const startCalendar = startPicker.locator(DATEPICKER_CALENDAR_SELECTOR);
    await expect(startCalendar).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });

    // Open the end date picker (should close the first)
    const endTrigger = endPicker.locator(DATEPICKER_TRIGGER_SELECTOR);
    await endTrigger.click();

    // Wait for the end date calendar to appear and verify the start one closed
    const endCalendar = endPicker.locator(DATEPICKER_CALENDAR_SELECTOR);
    await expect(endCalendar).toBeVisible({ timeout: INTERACTION_TIMEOUT_MS });
    await expect(startCalendar).toHaveCount(0);

    // There should only be one calendar visible at a time
    const visibleCalendars = page.locator(DATEPICKER_CALENDAR_SELECTOR);
    await expect(visibleCalendars).toHaveCount(1);
  });
});

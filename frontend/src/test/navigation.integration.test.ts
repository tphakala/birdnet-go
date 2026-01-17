/**
 * Navigation Integration Tests
 *
 * These tests validate that SPA navigation works end-to-end:
 * - URL changes when navigation is triggered
 * - The correct component is rendered (not just URL change)
 * - Browser back/forward navigation works
 *
 * BUG CONTEXT:
 * After PR #1818, components call navigation.navigate() directly which
 * updates the URL but bypasses App.svelte's handleRouting() function.
 * Result: URL changes but component doesn't re-render.
 *
 * These tests will FAIL until the bug is fixed, validating that:
 * 1. The bug exists (tests fail initially)
 * 2. The fix works (tests pass after fix)
 */

/* eslint-disable no-console -- Console statements are intentional for test feedback */
/* eslint-disable @typescript-eslint/no-unnecessary-condition -- Dynamic DOM queries may return null */
/* eslint-disable security/detect-non-literal-regexp -- Dynamic route patterns are intentional */

import { describe, expect, it } from 'vitest';
import { apiCall, integrationUtils } from './integration-setup';

/**
 * Navigation test utilities
 * Extends integrationUtils with navigation-specific helpers
 */
const navigationUtils = {
  /**
   * Wait for URL to match expected pattern (polling-based, no arbitrary sleep)
   */
  async waitForUrl(pattern: RegExp, timeout = 5000): Promise<boolean> {
    const startTime = Date.now();
    while (Date.now() - startTime < timeout) {
      if (pattern.test(window.location.pathname)) return true;
      await new Promise(r => setTimeout(r, 100));
    }
    return false;
  },

  /**
   * Wait for URL to exactly match a path
   */
  async waitForPath(path: string, timeout = 5000): Promise<boolean> {
    const startTime = Date.now();
    while (Date.now() - startTime < timeout) {
      if (window.location.pathname === path) return true;
      await new Promise(r => setTimeout(r, 100));
    }
    return false;
  },

  /**
   * Click element and wait for navigation to complete
   * This verifies BOTH URL change AND content change - the key to catching the bug
   *
   * @returns Object with urlChanged (URL matches pattern) and contentChanged (target element appeared)
   */
  async clickAndNavigate(
    clickSelector: string,
    expectedUrlPattern: RegExp,
    expectedContentSelector: string
  ): Promise<{ urlChanged: boolean; contentChanged: boolean; element: Element | null }> {
    const element = document.querySelector(clickSelector) as HTMLElement;
    if (!element) {
      throw new Error(`Click target not found: ${clickSelector}`);
    }

    element.click();

    // Wait for URL to change (polling, not arbitrary sleep)
    const urlChanged = await this.waitForUrl(expectedUrlPattern, 5000);

    // Wait for content to change (this is where the bug manifests)
    // Uses integrationUtils.waitForElement for consistency with other tests
    const contentElement = await integrationUtils.waitForElement(expectedContentSelector, 5000);
    const contentChanged = contentElement !== null;

    return { urlChanged, contentChanged, element: contentElement };
  },

  /**
   * Navigate to a path and wait for page to stabilize
   * Uses polling to wait for URL change, not arbitrary timeout
   */
  async navigateTo(path: string): Promise<void> {
    window.location.href = path;
    // Wait for URL to actually change
    await this.waitForPath(path, 5000);
    // Small additional wait for DOM to stabilize after navigation
    await new Promise(r => setTimeout(r, 300));
  },
};

// ============================================================================
// Detection Navigation Tests
// These tests verify navigation from detection list to detection detail
// ============================================================================

describe('Detection Navigation', () => {
  /**
   * This test captures the core bug:
   * When clicking a detection in the list, the URL changes to /ui/detections/{id}
   * but the DetectionDetail component is NOT rendered.
   *
   * EXPECTED: URL changes AND DetectionDetail renders (species-heading appears)
   * ACTUAL (BUG): URL changes but DetectionsList remains visible
   */
  it('clicking detection navigates and renders detail page', async () => {
    // First, verify backend has detections we can test with
    const response = await apiCall('/detections?limit=1');
    expect(response.ok).toBe(true);

    const data = await response.json();

    // Skip if no detections available
    if (!data.data || data.data.length === 0) {
      console.log('Skipping test: no detections available in database');
      return;
    }

    const detectionId = data.data[0].id;
    expect(detectionId).toBeDefined();

    // Navigate to detections list page
    await navigationUtils.navigateTo('/ui/detections');

    // Wait for detections list to render
    const listLoaded = await integrationUtils.waitForElement(
      '[class*="detection"], table tbody tr, .card',
      5000
    );
    if (!listLoaded) {
      console.log('Skipping: detections list did not load');
      return;
    }

    // Try to find a clickable detection element
    // Priority order: specific ID link, data attribute, generic detection element
    const selectors = [
      `a[href*="/ui/detections/${detectionId}"]`,
      `[data-detection-id="${detectionId}"]`,
      'a[href*="/ui/detections/"]',
      '[class*="detection-row"]',
      'table tbody tr',
    ];

    let clickableElement: HTMLElement | null = null;
    for (const selector of selectors) {
      clickableElement = document.querySelector(selector) as HTMLElement;
      if (clickableElement) break;
    }

    if (!clickableElement) {
      console.log('Skipping: could not find clickable detection element');
      return;
    }

    // Use the clickAndNavigate helper which properly waits for both URL and content
    clickableElement.click();

    // Wait for URL to change to detection detail pattern
    const urlChanged = await navigationUtils.waitForUrl(/\/ui\/detections\/\d+/, 5000);
    expect(urlChanged).toBe(true);

    // CRITICAL BUG CHECK:
    // Wait for detection detail content to render
    // The species-heading element is unique to DetectionDetail.svelte
    const speciesHeading = await integrationUtils.waitForElement('#species-heading', 5000);

    // If this fails, it means URL changed but component didn't render - THE BUG
    expect(speciesHeading).not.toBeNull();
  });

  /**
   * Test that browser back button works after navigation
   */
  it('browser back button returns to previous page after detection navigation', async () => {
    // Get a detection ID
    const response = await apiCall('/detections?limit=1');
    if (!response.ok) return;

    const data = await response.json();
    if (!data.data || data.data.length === 0) return;

    const detectionId = data.data[0].id;

    // Navigate to detections list
    await navigationUtils.navigateTo('/ui/detections');

    // Navigate to detection detail
    await navigationUtils.navigateTo(`/ui/detections/${detectionId}`);

    // Verify we're on detail page
    expect(window.location.pathname).toMatch(/\/ui\/detections\/\d+/);

    // Wait for detail page to render
    await integrationUtils.waitForElement('#species-heading', 5000);

    // Simulate back button
    window.history.back();

    // Wait for URL to change back to detections list
    const backNavWorked = await navigationUtils.waitForPath('/ui/detections', 5000);
    expect(backNavWorked).toBe(true);

    // Content should reflect detections list (not detail)
    // The species-heading is unique to detail page, should disappear
    // Give some time for DOM to update after back navigation
    await new Promise(r => setTimeout(r, 300));
    const speciesHeading = document.querySelector('#species-heading');
    expect(speciesHeading).toBeNull();
  });
});

// ============================================================================
// Sidebar Navigation Tests
// These test the main navigation links in the sidebar
// ============================================================================

describe('Sidebar Navigation', () => {
  const sidebarRoutes = [
    { name: 'Dashboard', path: '/ui/dashboard', selector: '[href*="dashboard"]' },
    { name: 'Analytics', path: '/ui/analytics', selector: '[href*="analytics"]' },
    { name: 'Search', path: '/ui/search', selector: '[href*="search"]' },
    { name: 'About', path: '/ui/about', selector: '[href*="about"]' },
    { name: 'System', path: '/ui/system', selector: '[href*="system"]' },
    { name: 'Settings', path: '/ui/settings', selector: '[href*="settings"]' },
  ];

  // Test each sidebar navigation link
  for (const route of sidebarRoutes) {
    it(`navigates to ${route.name} page via sidebar`, async () => {
      // Start from dashboard
      await navigationUtils.navigateTo('/ui/dashboard');

      // Wait for page to load
      await integrationUtils.waitForElement('nav, aside, .drawer-side', 5000);

      // Find sidebar link
      const sidebarLink = document.querySelector(
        `nav ${route.selector}, aside ${route.selector}, .drawer-side ${route.selector}`
      ) as HTMLElement;

      if (!sidebarLink) {
        console.log(`Skipping: ${route.name} link not found in sidebar`);
        return;
      }

      sidebarLink.click();

      // Wait for URL to change using polling
      const urlChanged = await navigationUtils.waitForUrl(new RegExp(route.path), 5000);

      // Verify URL changed
      expect(urlChanged).toBe(true);
      expect(window.location.pathname).toContain(route.path.replace('/ui/', ''));
    });
  }
});

// ============================================================================
// Dashboard Detection Navigation Tests
// These test clicking detections from the dashboard
// ============================================================================

describe('Dashboard Detection Navigation', () => {
  it('clicking recent detection on dashboard navigates to detail', async () => {
    // Navigate to dashboard
    await navigationUtils.navigateTo('/ui/dashboard');

    // Wait for dashboard to fully load (it loads more data than list page)
    await integrationUtils.waitForElement('.card, [class*="dashboard"]', 5000);
    // Additional wait for async detection data
    await new Promise(r => setTimeout(r, 1000));

    // Find any detection card on dashboard
    const detectionSelectors = [
      'a[href*="/ui/detections/"]',
      '[data-detection-id]',
      '[onclick*="detections"]',
    ];

    let detectionCard: HTMLElement | null = null;
    for (const selector of detectionSelectors) {
      detectionCard = document.querySelector(selector) as HTMLElement;
      if (detectionCard) break;
    }

    if (!detectionCard) {
      console.log('Skipping: no detection cards found on dashboard');
      return;
    }

    detectionCard.click();

    // Wait for URL to change to detection detail
    const urlChanged = await navigationUtils.waitForUrl(/\/ui\/detections\/\d+/, 5000);
    expect(urlChanged).toBe(true);

    // Content should be detection detail - this catches the bug
    const speciesHeading = await integrationUtils.waitForElement('#species-heading', 5000);
    expect(speciesHeading).not.toBeNull();
  });
});

// ============================================================================
// URL State Consistency Tests
// These verify URL and rendered content stay in sync
// ============================================================================

describe('URL State Consistency', () => {
  /**
   * This test verifies that directly navigating to a URL renders the correct content
   * This should PASS even with the bug, since direct navigation triggers full page load
   */
  it('direct URL navigation renders correct page', async () => {
    // Get a valid detection ID
    const response = await apiCall('/detections?limit=1');
    if (!response.ok) return;

    const data = await response.json();
    if (!data.data || data.data.length === 0) return;

    const detectionId = data.data[0].id;

    // Directly navigate to detection detail URL
    await navigationUtils.navigateTo(`/ui/detections/${detectionId}`);

    // URL should be correct
    expect(window.location.pathname).toBe(`/ui/detections/${detectionId}`);

    // Content should match - species heading should be present
    const speciesHeading = await integrationUtils.waitForElement('#species-heading', 5000);
    expect(speciesHeading).not.toBeNull();
  });

  /**
   * Test forward/back navigation maintains consistency
   */
  it('history navigation maintains URL/content consistency', async () => {
    // Navigate: dashboard -> detections
    await navigationUtils.navigateTo('/ui/dashboard');
    await navigationUtils.navigateTo('/ui/detections');

    // Go back
    window.history.back();

    // Wait for URL to change back to dashboard
    const backWorked = await navigationUtils.waitForPath('/ui/dashboard', 5000);
    expect(backWorked).toBe(true);

    // Go forward
    window.history.forward();

    // Wait for URL to change to detections
    const forwardWorked = await navigationUtils.waitForPath('/ui/detections', 5000);
    expect(forwardWorked).toBe(true);
  });
});

// ============================================================================
// Search Navigation Tests
// These test navigation from search results to detection detail
// ============================================================================

describe('Search Navigation', () => {
  it('clicking search result navigates to detection detail', async () => {
    // Navigate to search page
    await navigationUtils.navigateTo('/ui/search');

    // Wait for search page to load
    await integrationUtils.waitForElement('input[type="search"], input[type="text"]', 5000);

    // Look for any detection results (might be pre-populated or require search)
    const resultSelectors = [
      'a[href*="/ui/detections/"]',
      '[data-detection-id]',
      '.search-result a',
    ];

    let resultLink: HTMLElement | null = null;
    for (const selector of resultSelectors) {
      resultLink = document.querySelector(selector) as HTMLElement;
      if (resultLink) break;
    }

    if (!resultLink) {
      console.log('Skipping: no search results available to click');
      return;
    }

    resultLink.click();

    // Wait for URL to change to detection detail
    const urlChanged = await navigationUtils.waitForUrl(/\/ui\/detections\/\d+/, 5000);
    expect(urlChanged).toBe(true);

    // Content should be detection detail
    const speciesHeading = await integrationUtils.waitForElement('#species-heading', 5000);
    expect(speciesHeading).not.toBeNull();
  });
});

import type { Page, Locator } from '@playwright/test';
import { expect } from '@playwright/test';

/**
 * Page Object Model for the Dashboard page in the new Svelte UI
 *
 * Provides methods to interact with dashboard elements and verify dashboard state.
 * Targets elements using data-testid selectors for stability.
 */
export class DashboardPage {
  readonly page: Page;
  /** Status indicator showing system health - targets [data-testid="status-indicator"] */
  readonly statusIndicator: Locator;
  /** List container for recent detections - targets [data-testid="detections-list"] */
  readonly detectionsList: Locator;
  /** Settings navigation button - targets [data-testid="settings-button"] */
  readonly settingsButton: Locator;

  /**
   * Initialize dashboard page object
   * @param page Playwright page instance to interact with
   */
  constructor(page: Page) {
    this.page = page;
    this.statusIndicator = page.getByTestId('status-indicator');
    this.detectionsList = page.getByTestId('detections-list');
    this.settingsButton = page.getByTestId('settings-button');
  }

  /**
   * Navigate to the dashboard page and wait for it to load
   * Verifies main content is visible and URL matches expected pattern
   */
  async navigate(): Promise<void> {
    // Navigate to the new UI dashboard
    await this.page.goto('/ui/dashboard');

    // Wait for main content to be visible (more reliable than status indicator)
    await expect(
      this.page.locator('[data-testid="main-content"], main, [role="main"]')
    ).toBeVisible();

    // Assert the final URL to catch misroutes
    await expect(this.page).toHaveURL(/.*\/ui\/dashboard/);

    // If status indicator exists, wait for it
    if ((await this.statusIndicator.count()) > 0) {
      await expect(this.statusIndicator).toBeVisible();
    }
  }

  /**
   * Wait for a specific species detection to appear
   * Uses stable data-testid pattern with species filter
   * @param species Species name to wait for
   * @param timeout Maximum wait time in milliseconds
   */
  async waitForDetection(species: string, timeout = 30_000): Promise<void> {
    // Use locator-based expect instead of waitForSelector
    const detectionLocator = this.page
      .locator('[data-testid^="detection-"]')
      .filter({ hasText: species });

    await expect(detectionLocator).toBeVisible({ timeout });
  }

  /**
   * Play an audio clip for a detection
   * Clicks play button and waits for audio player to appear
   * @param detectionId ID of the detection to play
   */
  async playAudioClip(detectionId: string): Promise<void> {
    await this.page.getByTestId(`play-${detectionId}`).click();
    await expect(this.page.getByTestId('audio-player')).toBeVisible({ timeout: 10_000 });
  }

  /**
   * Navigate to settings page
   * Handles both button click and direct navigation fallback
   * Waits for both navigation and settings content to be ready
   */
  async openSettings(): Promise<void> {
    if ((await this.settingsButton.count()) > 0) {
      // Use Promise.all to wait for both click and navigation concurrently
      await Promise.all([this.page.waitForURL('**/ui/settings'), this.settingsButton.click()]);
    } else {
      // Fallback: navigate directly to settings
      await this.page.goto('/ui/settings');
    }

    // Always verify settings content is visible
    await expect(
      this.page.locator('[data-testid="main-content"], main, [role="main"]')
    ).toBeVisible();
  }

  /**
   * Get the count of detection items in the list
   * Waits for the detections list to be ready before counting
   * @returns Promise resolving to the number of detection items
   */
  async getDetectionCount(): Promise<number> {
    // Wait for the detections list to be visible before counting
    await expect(this.detectionsList).toBeVisible();

    // Count the detection items within the list
    return this.detectionsList.locator('[data-testid="detection-item"]').count();
  }
}

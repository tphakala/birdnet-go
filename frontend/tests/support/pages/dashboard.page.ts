import type { Page, Locator } from '@playwright/test';
import { expect } from '@playwright/test';

export class DashboardPage {
  readonly page: Page;
  readonly statusIndicator: Locator;
  readonly detectionsList: Locator;
  readonly settingsButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.statusIndicator = page.locator('[data-testid="status-indicator"]');
    this.detectionsList = page.locator('[data-testid="detections-list"]');
    this.settingsButton = page.locator('[data-testid="settings-button"]');
  }

  async navigate() {
    // Navigate to the new UI dashboard
    await this.page.goto('/ui/dashboard');

    // Wait for main content to be visible (more reliable than status indicator)
    await expect(
      this.page.locator('[data-testid="main-content"], main, [role="main"]')
    ).toBeVisible();

    // If status indicator exists, wait for it
    if ((await this.statusIndicator.count()) > 0) {
      await expect(this.statusIndicator).toBeVisible();
    }
  }

  async waitForDetection(species: string, timeout: number = 30000) {
    await this.page.waitForSelector(`[data-testid="detection-${species}"]`, { timeout });
  }

  async playAudioClip(detectionId: string) {
    await this.page.click(`[data-testid="play-${detectionId}"]`);
    await expect(this.page.locator('[data-testid="audio-player"]')).toBeVisible();
  }

  async openSettings() {
    if ((await this.settingsButton.count()) > 0) {
      await this.settingsButton.click();
      await this.page.waitForURL('**/ui/settings');
    } else {
      // Fallback: navigate directly to settings
      await this.page.goto('/ui/settings');
      await expect(
        this.page.locator('[data-testid="main-content"], main, [role="main"]')
      ).toBeVisible();
    }
  }

  async getDetectionCount(): Promise<number> {
    return await this.detectionsList.locator('[data-testid="detection-item"]').count();
  }
}

/**
 * Comprehensive accessibility test suite for BirdNET-Go frontend components
 * Tests WCAG 2.1 Level AA compliance using axe-core
 */
import { describe, it, beforeEach, afterEach } from 'vitest';
import { expectNoA11yViolations, getA11yReport, A11Y_CONFIGS } from '$lib/utils/axe-utils';

describe('Frontend Accessibility Tests', () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    container = document.createElement('div');
    container.id = 'test-container';
    document.body.appendChild(container);
  });

  afterEach(() => {
    document.body.removeChild(container);
  });

  describe('Form Components Accessibility', () => {
    it('should pass accessibility test for form with proper labels', async () => {
      container.innerHTML = `
        <form>
          <label for="username">Username</label>
          <input type="text" id="username" name="username" required>
          
          <label for="email">Email</label>
          <input type="email" id="email" name="email" required>
          
          <button type="submit">Submit</button>
        </form>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.forms);
      expect(container.querySelector('form')).toBeTruthy();
    });

    it('should pass accessibility test for select dropdown with proper labeling', async () => {
      container.innerHTML = `
        <div class="select-dropdown">
          <label class="label" for="select-dropdown-test" id="select-dropdown-test-label">
            <span class="label-text">Choose an option</span>
          </label>
          <button
            id="select-dropdown-test"
            type="button"
            class="btn btn-block justify-between"
            aria-haspopup="listbox"
            aria-expanded="false"
            aria-labelledby="select-dropdown-test-label"
          >
            Select an option
          </button>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.forms);
      expect(container.querySelector('.select-dropdown')).toBeTruthy();
    });

    it('should pass accessibility test for checkbox with proper labeling', async () => {
      container.innerHTML = `
        <div class="form-control">
          <label class="label cursor-pointer">
            <input type="checkbox" class="checkbox" id="test-checkbox">
            <span class="label-text">I agree to the terms</span>
          </label>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.forms);
      expect(container.querySelector('.form-control')).toBeTruthy();
    });
  });

  describe('Navigation and Interactive Elements', () => {
    it('should pass accessibility test for button with proper labeling', async () => {
      container.innerHTML = `
        <button type="button" aria-label="Close dialog" class="btn btn-sm btn-circle">
          <svg aria-hidden="true" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);

      // Verify specific ARIA attributes
      const button = container.querySelector('button');
      expect(button?.getAttribute('aria-label')).toBe('Close dialog');

      const svg = container.querySelector('svg');
      expect(svg?.getAttribute('aria-hidden')).toBe('true');
    });

    it('should pass accessibility test for pagination controls', async () => {
      container.innerHTML = `
        <div aria-label="Pagination">
          <div class="join">
            <button class="join-item btn btn-sm" aria-label="Go to previous page" disabled>«</button>
            <button class="join-item btn btn-sm btn-active" aria-label="Current page">Page 1 of 5</button>
            <button class="join-item btn btn-sm" aria-label="Go to next page">»</button>
          </div>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);

      // Verify specific ARIA attributes
      const paginationContainer = container.querySelector('div[aria-label="Pagination"]');
      expect(paginationContainer?.getAttribute('aria-label')).toBe('Pagination');

      const prevButton = container.querySelector('button[aria-label="Go to previous page"]');
      expect(prevButton?.getAttribute('aria-label')).toBe('Go to previous page');
      expect(prevButton?.hasAttribute('disabled')).toBe(true);

      const currentButton = container.querySelector('button[aria-label="Current page"]');
      expect(currentButton?.getAttribute('aria-label')).toBe('Current page');

      const nextButton = container.querySelector('button[aria-label="Go to next page"]');
      expect(nextButton?.getAttribute('aria-label')).toBe('Go to next page');
    });

    it('should pass accessibility test for data table with proper headers', async () => {
      container.innerHTML = `
        <table class="table" role="table">
          <thead>
            <tr>
              <th aria-sort="none">Species</th>
              <th aria-sort="none">Confidence</th>
              <th aria-sort="none">Time</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>American Robin</td>
              <td>0.95</td>
              <td>12:30 PM</td>
            </tr>
          </tbody>
        </table>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);

      // Verify specific ARIA attributes on table headers
      const table = container.querySelector('table[role="table"]');
      expect(table?.getAttribute('role')).toBe('table');

      const headers = container.querySelectorAll('th[aria-sort]');
      expect(headers).toHaveLength(3);

      const speciesHeader = container.querySelector('th:first-child');
      expect(speciesHeader?.getAttribute('aria-sort')).toBe('none');
      expect(speciesHeader?.textContent).toBe('Species');

      const confidenceHeader = container.querySelector('th:nth-child(2)');
      expect(confidenceHeader?.getAttribute('aria-sort')).toBe('none');
      expect(confidenceHeader?.textContent).toBe('Confidence');

      const timeHeader = container.querySelector('th:nth-child(3)');
      expect(timeHeader?.getAttribute('aria-sort')).toBe('none');
      expect(timeHeader?.textContent).toBe('Time');
    });
  });

  describe('Modal and Dialog Accessibility', () => {
    it('should pass accessibility test for modal dialog', async () => {
      container.innerHTML = `
        <div class="modal modal-open" role="dialog" aria-modal="true" aria-labelledby="modal-title">
          <div class="modal-box" tabindex="-1">
            <button type="button" class="btn btn-sm btn-circle btn-ghost absolute right-2 top-2" aria-label="Close modal">
              <svg aria-hidden="true" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
            <h3 id="modal-title" class="font-bold text-lg mb-4">Confirm Action</h3>
            <div class="py-4">
              <p>Are you sure you want to proceed?</p>
            </div>
            <div class="modal-action">
              <button type="button" class="btn btn-ghost">Cancel</button>
              <button type="button" class="btn btn-primary">Confirm</button>
            </div>
          </div>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);
      expect(container.innerHTML).toContain('aria');
    });

    it('should pass accessibility test for dropdown menu', async () => {
      container.innerHTML = `
        <div class="relative">
          <button
            type="button"
            class="btn"
            aria-expanded="true"
            aria-haspopup="true"
            aria-label="Audio level for Default Source"
          >
            Audio Sources
          </button>
          <div role="menu" aria-label="Audio Source Selection" class="absolute">
            <div class="py-1" role="menu" aria-orientation="vertical">
              <button role="menuitemradio" aria-checked="true" class="w-full text-left">
                Default Source
              </button>
              <button role="menuitemradio" aria-checked="false" class="w-full text-left">
                Secondary Source
              </button>
            </div>
          </div>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);
      expect(container.innerHTML).toContain('aria');
    });
  });

  describe('Status and Notification Elements', () => {
    it('should pass accessibility test for status notifications with live regions', async () => {
      container.innerHTML = `
        <div>
          <div role="status" aria-live="polite" class="sr-only">
            Current audio level: 75 percent
          </div>
          <div role="status" aria-live="polite">
            <div class="alert alert-success">
              <span>Settings saved successfully</span>
            </div>
          </div>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);
      expect(container.innerHTML).toContain('aria');
    });

    it('should pass accessibility test for notification list', async () => {
      container.innerHTML = `
        <div role="region" aria-label="Notifications list">
          <article class="card">
            <div class="card-body">
              <div class="flex items-start gap-4">
                <div class="flex-shrink-0">
                  <div class="w-10 h-10 rounded-full flex items-center justify-center bg-error/20 text-error">
                    <svg aria-hidden="true" class="h-6 w-6 shrink-0 stroke-current" fill="none" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </div>
                </div>
                <div class="flex-1">
                  <h3 class="font-semibold text-lg">System Error</h3>
                  <p class="text-base-content/80 mt-1">Unable to connect to audio device</p>
                  <div class="flex flex-wrap items-center gap-2 mt-3">
                    <span class="badge badge-error badge-sm">critical</span>
                    <time class="text-xs text-base-content/60" datetime="2023-12-07T10:30:00Z">
                      2 hours ago
                    </time>
                  </div>
                </div>
                <div class="flex items-center gap-2">
                  <button class="btn btn-ghost btn-xs" aria-label="Mark as read">
                    <svg aria-hidden="true" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                    </svg>
                  </button>
                </div>
              </div>
            </div>
          </article>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);
      expect(container.innerHTML).toContain('aria');
    });
  });

  describe('Media and Interactive Controls', () => {
    it('should pass accessibility test for audio player controls', async () => {
      container.innerHTML = `
        <div class="audio-player" role="region" aria-label="Audio player">
          <audio controls aria-label="Bird detection audio clip">
            <source src="/test-audio.mp3" type="audio/mpeg">
            Your browser does not support the audio element.
          </audio>
          <div class="audio-controls">
            <button type="button" aria-label="Play audio" class="btn">
              <svg aria-hidden="true" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
              </svg>
            </button>
            <button type="button" aria-label="Download audio" class="btn">
              <svg aria-hidden="true" class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
              </svg>
            </button>
          </div>
        </div>
      `;

      await expectNoA11yViolations(container, A11Y_CONFIGS.strict);
      expect(container.innerHTML).toContain('aria');
    });
  });

  describe('Complex Component Integration', () => {
    it('should generate accessibility report for complex dashboard layout', async () => {
      container.innerHTML = `
        <main role="main">
          <header>
            <nav role="navigation" aria-label="Main navigation">
              <ul>
                <li><a href="/dashboard" aria-current="page">Dashboard</a></li>
                <li><a href="/detections">Detections</a></li>
                <li><a href="/settings">Settings</a></li>
              </ul>
            </nav>
          </header>
          
          <section aria-labelledby="recent-detections-heading">
            <h2 id="recent-detections-heading">Recent Detections</h2>
            <div class="card">
              <div class="card-body">
                <table class="table" role="table">
                  <caption class="sr-only">Recent bird detections with species, confidence, and timestamp</caption>
                  <thead>
                    <tr>
                      <th scope="col">Species</th>
                      <th scope="col">Confidence</th>
                      <th scope="col">Time</th>
                      <th scope="col">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>American Robin</td>
                      <td>95%</td>
                      <td>2 minutes ago</td>
                      <td>
                        <button type="button" aria-label="Play audio for American Robin detection" class="btn btn-sm">
                          <svg aria-hidden="true" class="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
                            <path d="M8 5v10l8-5-8-5z"/>
                          </svg>
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </section>
        </main>
      `;

      // Generate and log report (don't assert violations for complex layouts)
      const report = await getA11yReport(container, A11Y_CONFIGS.strict);

      // Only log report in development/local environment to keep CI/CD logs clean
      const nodeEnv = typeof globalThis.process !== 'undefined' ? globalThis.process.env.NODE_ENV : undefined;
      const isCI = typeof globalThis.process !== 'undefined' ? globalThis.process.env.CI : undefined;

      if (nodeEnv === 'development' || (nodeEnv === 'test' && !isCI)) {
        // eslint-disable-next-line no-console
        console.log('Dashboard Accessibility Report:\n', report);
      }

      expect(report).toContain('Accessibility Test Results');
    });
  });
});

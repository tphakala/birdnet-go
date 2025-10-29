/**
 * Binding Validation Tests for Svelte 5 Fixes
 *
 * This test suite validates that the Svelte 5 binding fixes are working correctly
 * by ensuring that:
 *
 * 1. All settings pages render without binding-related console errors
 * 2. Form interactions work without throwing errors
 * 3. No "non-reactive" warnings are logged when using forms
 * 4. Components use proper one-way binding patterns with event handlers
 *
 * These tests verify that the migration from bind:value/bind:checked on $derived
 * objects to value=/checked= with onchange handlers works correctly.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { INIT_TIMEOUT, STATE_UPDATE_TIMEOUT, wait } from '$lib/../test/constants';

// Mock external dependencies to prevent network calls and complex integrations
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ data: [] }),
    post: vi.fn().mockResolvedValue({ data: {} }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    data?: unknown;
    constructor(message: string, status: number, data?: unknown) {
      super(message);
      this.status = status;
      this.data = data;
    }
  },
}));

vi.mock('$lib/stores/toast', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/utils/logger', () => ({
  loggers: {
    settings: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
    audio: {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    },
  },
}));

vi.mock('maplibre-gl', () => ({
  default: {
    Map: vi.fn(),
    Marker: vi.fn(),
  },
}));

describe('Settings Binding Validation - Svelte 5 Fixes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Component Rendering', () => {
    const settingsPages = [
      { name: 'MainSettingsPage', path: './MainSettingsPage.svelte', checkFormElements: true },
      { name: 'AudioSettingsPage', path: './AudioSettingsPage.svelte', checkFormElements: false },
      { name: 'FilterSettingsPage', path: './FilterSettingsPage.svelte', checkFormElements: false },
      {
        name: 'SecuritySettingsPage',
        path: './SecuritySettingsPage.svelte',
        checkFormElements: false,
      },
    ];

    settingsPages.forEach(({ name, path, checkFormElements }) => {
      it(`${name} renders without binding-related errors`, async () => {
        const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
        const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

        try {
          const SettingsPageModule = await import(path);
          const { component } = render(SettingsPageModule.default);

          // Component should render successfully
          expect(component).toBeTruthy();

          // Check for form elements if required (MainSettingsPage specific)
          if (checkFormElements) {
            const inputs = screen.queryAllByRole('textbox');
            const checkboxes = screen.queryAllByRole('checkbox');
            const selects = screen.queryAllByRole('combobox');

            // At least one type of form element should exist
            expect(inputs.length + checkboxes.length + selects.length).toBeGreaterThan(0);
          }

          // Check that no binding-related errors were logged
          const errorCalls = consoleSpy.mock.calls;
          const warnCalls = consoleWarnSpy.mock.calls;

          const bindingErrors = [...errorCalls, ...warnCalls].filter(
            ([message]) =>
              message &&
              typeof message === 'string' &&
              (message.includes('bind:') ||
                message.includes('non-reactive') ||
                message.includes('derived'))
          );

          expect(bindingErrors).toHaveLength(0);
        } finally {
          consoleSpy.mockRestore();
          consoleWarnSpy.mockRestore();
        }
      });
    });
  });

  describe('Form Interaction Patterns', () => {
    it('Text inputs can be interacted with without errors', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Find text inputs - getAllByRole will fail if none found
        const inputs = screen.getAllByRole('textbox');

        // Interact with the first input
        const firstInput = inputs[0] as HTMLInputElement;
        await fireEvent.change(firstInput, { target: { value: 'test-value' } });

        // Should not cause any console errors
        expect(consoleSpy).not.toHaveBeenCalled();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('Checkboxes can be interacted with without errors', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Find checkboxes - use getAllByRole to fail loudly if none found
        const checkboxes = screen.getAllByRole('checkbox');

        // Click the first checkbox
        await fireEvent.click(checkboxes[0]);

        // Should not cause any console errors
        expect(consoleSpy).not.toHaveBeenCalled();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('Select fields can be interacted with without errors', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Find select elements - getAllByRole will fail if none found
        const selects = screen.getAllByRole('combobox');

        // Change the first select
        await fireEvent.change(selects[0], { target: { value: 'mysql' } });

        // Should not cause any console errors
        expect(consoleSpy).not.toHaveBeenCalled();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('Number inputs respond to value changes', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Wait for component to fully initialize
        await wait(INIT_TIMEOUT);

        // Find number inputs - getAllByRole will fail if none found
        const numberInputs = screen.getAllByRole('spinbutton');
        expect(numberInputs.length).toBeGreaterThan(0);

        const numberInput = numberInputs[0] as HTMLInputElement;

        // Use userEvent for more realistic user interaction
        const user = userEvent.setup();

        // Clear and type new value
        await user.clear(numberInput);
        await user.type(numberInput, '42');

        // Tab away to trigger blur
        await user.tab();

        // Wait for all reactive updates to complete
        await wait(STATE_UPDATE_TIMEOUT);

        // Primary test: should not cause any console errors during binding interactions
        expect(consoleSpy).not.toHaveBeenCalled();

        // Secondary test: input should maintain a valid numeric value (store may override user input)
        const currentValue = parseFloat(numberInput.value);
        expect(isNaN(currentValue)).toBe(false); // Should remain a valid number

        // Ensure input is still functional (not broken by reactive binding)
        expect(numberInput.disabled).toBe(false);
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('Form elements maintain proper accessibility attributes', async () => {
      const MainSettingsPage = await import('./MainSettingsPage.svelte');
      render(MainSettingsPage.default);

      // Check that form elements have proper labels
      // Using queryAll since some pages might not have all element types
      const inputs = screen.queryAllByRole('textbox');
      const checkboxes = screen.queryAllByRole('checkbox');
      const selects = screen.queryAllByRole('combobox');

      // Count elements that have proper labeling
      let properlyLabeledCount = 0;
      const totalElements = inputs.length + checkboxes.length + selects.length;

      // Ensure at least some form elements exist to test
      expect(totalElements).toBeGreaterThan(0);

      [...inputs, ...checkboxes, ...selects].forEach(element => {
        const hasExplicitLabel = element.id && document.querySelector(`label[for="${element.id}"]`);
        const hasImplicitLabel = element.closest('label');
        const hasAriaLabel = element.getAttribute('aria-label');
        const hasAriaLabelledBy = element.getAttribute('aria-labelledby');

        if (hasExplicitLabel || hasImplicitLabel || hasAriaLabel || hasAriaLabelledBy) {
          properlyLabeledCount++;
        }
      });

      // Most elements should be properly labeled (allow some flexibility)
      const labelingPercentage = properlyLabeledCount / totalElements;
      expect(labelingPercentage).toBeGreaterThan(0.7); // At least 70% should be labeled
    });
  });

  describe('Binding Pattern Validation', () => {
    it('validates that derived objects are not bound with bind: directive', async () => {
      // This test validates that our fixes are in place by checking that:
      // 1. Components render without binding errors
      // 2. No "non-reactive" warnings are logged
      // 3. Form interactions work without throwing errors

      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      try {
        // Test all the pages we fixed
        const pages = [
          './MainSettingsPage.svelte',
          './AudioSettingsPage.svelte',
          './FilterSettingsPage.svelte',
          './IntegrationSettingsPage.svelte',
          './SecuritySettingsPage.svelte',
          './SupportSettingsPage.svelte',
        ];

        for (const pagePath of pages) {
          const Page = await import(pagePath);
          const { component, unmount } = render(Page.default);

          // Component should render
          expect(component).toBeTruthy();

          // INTENTIONAL: Using queryAll + conditional checks here because we're iterating
          // through multiple different pages that have varying form elements.
          // Not all pages have all element types, so we need to handle missing elements gracefully.
          // This is testing that ANY page can handle interactions without errors, not that
          // specific elements exist on every page.
          const inputs = screen.queryAllByRole('textbox');
          const checkboxes = screen.queryAllByRole('checkbox');
          const selects = screen.queryAllByRole('combobox');

          // Try interacting with first element of each type if they exist
          if (inputs.length > 0) {
            await fireEvent.change(inputs[0], { target: { value: 'test' } });
          }
          if (checkboxes.length > 0) {
            await fireEvent.click(checkboxes[0]);
          }
          if (selects.length > 0) {
            await fireEvent.change(selects[0], { target: { value: 'test' } });
          }

          // Clean up
          unmount();
        }

        // Check that no binding-related errors occurred
        const allCalls = [...consoleSpy.mock.calls, ...consoleWarnSpy.mock.calls];
        const bindingIssues = allCalls.filter(
          ([message]) =>
            message &&
            typeof message === 'string' &&
            (message.includes('bind:') ||
              message.includes('non-reactive') ||
              message.includes('derived') ||
              message.includes('binding'))
        );

        expect(bindingIssues).toHaveLength(0);
      } finally {
        consoleSpy.mockRestore();
        consoleWarnSpy.mockRestore();
      }
    });
  });

  describe('Specific Binding Pattern Tests', () => {
    it('AudioSettingsPage uses one-way binding for all equalizer controls', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      try {
        const AudioSettingsPage = await import('./AudioSettingsPage.svelte');
        const { unmount } = render(AudioSettingsPage.default);

        // Find equalizer-related checkboxes and sliders
        const checkboxes = screen.queryAllByRole('checkbox');
        const sliders = screen.queryAllByRole('slider');

        // Test interactions with equalizer controls
        const equalizerCheckboxes = checkboxes.filter(
          checkbox =>
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            checkbox.getAttribute('id')?.includes('equalizer') ||
            checkbox.closest('label')?.textContent.toLowerCase().includes('equalizer')
        );

        for (const checkbox of equalizerCheckboxes) {
          await fireEvent.click(checkbox);
        }

        for (const slider of sliders) {
          await fireEvent.change(slider, { target: { value: '0.5' } });
        }

        // Should not produce any binding-related warnings
        const allCalls = [...consoleSpy.mock.calls, ...consoleWarnSpy.mock.calls];
        const bindingWarnings = allCalls.filter(
          ([message]) =>
            typeof message === 'string' &&
            (message.includes('non-reactive') || message.includes('bind:'))
        );

        expect(bindingWarnings).toHaveLength(0);

        unmount();
      } finally {
        consoleSpy.mockRestore();
        consoleWarnSpy.mockRestore();
      }
    });

    it('SecuritySettingsPage OAuth settings use proper onchange handlers', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
        const { unmount } = render(SecuritySettingsPage.default);

        // Find OAuth-related inputs (Google/GitHub auth)
        const textInputs = screen.queryAllByRole('textbox');
        const passwordInputs = screen.queryAllByDisplayValue('');

        // Test OAuth field interactions
        const oauthInputs = [...textInputs, ...passwordInputs].filter(
          input =>
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            input.getAttribute('id')?.includes('google') ||
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            input.getAttribute('id')?.includes('github') ||
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            input.getAttribute('id')?.includes('client') ||
            input.getAttribute('id')?.includes('secret')
        );

        for (const input of oauthInputs.slice(0, 3)) {
          // Test first 3 to avoid overloading
          await fireEvent.change(input, { target: { value: 'test-oauth-value' } });
        }

        // Should not cause console errors
        expect(consoleSpy).not.toHaveBeenCalled();

        unmount();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('MainSettingsPage coordinate inputs work with proper validation', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        const { unmount } = render(MainSettingsPage.default);

        // Find latitude/longitude number inputs
        const numberInputs = screen.queryAllByRole('spinbutton');

        // Test coordinate inputs with valid values
        const coordInputs = numberInputs.filter(
          input =>
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            input.getAttribute('id')?.includes('latitude') ||
            // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- Logical OR is correct here for falsy value handling
            input.getAttribute('id')?.includes('longitude') ||
            input.getAttribute('step') === '0.000001' // Coordinate precision
        );

        for (const input of coordInputs) {
          // Test valid coordinate values
          const isLatitude = input.getAttribute('id')?.includes('latitude');
          const testValue = isLatitude ? '40.7128' : '-74.0060';

          await fireEvent.change(input, { target: { value: testValue } });
          expect((input as HTMLInputElement).value).toBe(testValue);
        }

        // Should not cause console errors
        expect(consoleSpy).not.toHaveBeenCalled();

        unmount();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('FilterSettingsPage checkbox interactions work correctly', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const FilterSettingsPage = await import('./FilterSettingsPage.svelte');
        const { unmount } = render(FilterSettingsPage.default);

        // Find filter-related checkboxes
        const checkboxes = screen.queryAllByRole('checkbox');

        // Test all checkboxes (privacy filter, dog bark filter, etc.)
        for (const checkbox of checkboxes.slice(0, 5)) {
          const initialChecked = (checkbox as HTMLInputElement).checked;

          await fireEvent.click(checkbox);

          // Checkbox state should toggle
          expect((checkbox as HTMLInputElement).checked).toBe(!initialChecked);
        }

        // Should not cause console errors
        expect(consoleSpy).not.toHaveBeenCalled();

        unmount();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('IntegrationSettingsPage handles complex nested settings', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      try {
        const IntegrationSettingsPage = await import('./IntegrationSettingsPage.svelte');
        const { unmount } = render(IntegrationSettingsPage.default);

        // Find various integration inputs (MQTT, BirdWeather, etc.)
        const textInputs = screen.queryAllByRole('textbox');
        const numberInputs = screen.queryAllByRole('spinbutton');
        const checkboxes = screen.queryAllByRole('checkbox');

        // INTENTIONAL: Using conditional checks because IntegrationSettingsPage
        // has dynamic content based on which integrations are enabled.
        // Not all integration sections render all input types, so we test
        // whatever elements are actually present.
        if (textInputs.length > 0) {
          await fireEvent.change(textInputs[0], { target: { value: 'test-broker' } });
        }

        if (numberInputs.length > 0) {
          await fireEvent.change(numberInputs[0], { target: { value: '1883' } });
        }

        if (checkboxes.length > 0) {
          await fireEvent.click(checkboxes[0]);
        }

        // Should not cause console errors
        expect(consoleSpy).not.toHaveBeenCalled();

        unmount();
      } finally {
        consoleSpy.mockRestore();
      }
    });

    it('All settings pages handle rapid form interactions without errors', async () => {
      const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

      try {
        const pages = [
          './MainSettingsPage.svelte',
          './AudioSettingsPage.svelte',
          './SecuritySettingsPage.svelte',
        ];

        for (const pagePath of pages) {
          const Page = await import(pagePath);
          const { unmount } = render(Page.default);

          // Rapid fire interactions to test reactivity
          const inputs = screen.queryAllByRole('textbox').slice(0, 2);
          const checkboxes = screen.queryAllByRole('checkbox').slice(0, 2);

          // Rapid text input changes
          for (const input of inputs) {
            await fireEvent.change(input, { target: { value: 'rapid1' } });
            await fireEvent.change(input, { target: { value: 'rapid2' } });
            await fireEvent.change(input, { target: { value: 'rapid3' } });
          }

          // Rapid checkbox toggles
          for (const checkbox of checkboxes) {
            await fireEvent.click(checkbox);
            await fireEvent.click(checkbox);
            await fireEvent.click(checkbox);
          }

          unmount();
        }

        // Should handle rapid interactions without errors or warnings
        const allCalls = [...consoleSpy.mock.calls, ...consoleWarnSpy.mock.calls];
        const issues = allCalls.filter(
          ([message]) =>
            typeof message === 'string' &&
            (message.includes('bind:') ||
              message.includes('non-reactive') ||
              message.includes('error'))
        );

        expect(issues).toHaveLength(0);
      } finally {
        consoleSpy.mockRestore();
        consoleWarnSpy.mockRestore();
      }
    });
  });
});

/* eslint-disable @typescript-eslint/no-explicit-any */
/* eslint-disable @typescript-eslint/no-unnecessary-condition */
/**
 * Settings Validation and Boundary Conditions Test Suite
 *
 * This test suite validates that settings pages correctly handle:
 * - Input validation and boundary conditions
 * - Data type conversions and coercion
 * - Range limits and constraints
 * - Required field validation
 * - Cross-field dependencies
 *
 * Note: ESLint rules are disabled for this file because it intentionally tests
 * malformed data and edge cases that require using 'any' types and unnecessary conditions.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { expectNoA11yViolations, A11Y_CONFIGS } from '$lib/utils/axe-utils';
import {
  settingsStore,
  settingsActions,
  birdnetSettings,
  audioSettings,
  securitySettings,
} from '$lib/stores/settings';

// Note: Common mocks are now defined in src/test/setup.ts and loaded globally via Vitest configuration

describe('Settings Validation and Boundary Conditions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    settingsActions.resetAllSettings();
  });

  describe('Numeric Input Validation', () => {
    describe('MainSettingsPage - Coordinate Validation', () => {
      it('validates latitude bounds (-90 to 90)', async () => {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Query latitude input by label text - fails loudly if not found
        const latInput = screen.getByLabelText(/latitude/i, {
          selector: 'input[type="number"]',
        }) as HTMLInputElement;

        // Test valid values
        await fireEvent.change(latInput, { target: { value: '45.5' } });
        expect(latInput.value).toBe('45.5');

        await fireEvent.change(latInput, { target: { value: '-90' } });
        expect(latInput.value).toBe('-90');

        await fireEvent.change(latInput, { target: { value: '90' } });
        expect(latInput.value).toBe('90');

        // Test invalid values (should be constrained or rejected)
        await fireEvent.change(latInput, { target: { value: '91' } });
        // Value should be constrained to max
        expect(Number(latInput.value)).toBeLessThanOrEqual(90);

        await fireEvent.change(latInput, { target: { value: '-91' } });
        // Value should be constrained to min
        expect(Number(latInput.value)).toBeGreaterThanOrEqual(-90);
      });

      it('validates longitude bounds (-180 to 180)', async () => {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Query longitude input by label text - fails loudly if not found
        const lngInput = screen.getByLabelText(/longitude/i, {
          selector: 'input[type="number"]',
        }) as HTMLInputElement;

        // Test valid values
        await fireEvent.change(lngInput, { target: { value: '120.5' } });
        expect(lngInput.value).toBe('120.5');

        await fireEvent.change(lngInput, { target: { value: '-180' } });
        expect(lngInput.value).toBe('-180');

        await fireEvent.change(lngInput, { target: { value: '180' } });
        expect(lngInput.value).toBe('180');

        // Test invalid values
        await fireEvent.change(lngInput, { target: { value: '181' } });
        expect(Number(lngInput.value)).toBeLessThanOrEqual(180);

        await fireEvent.change(lngInput, { target: { value: '-181' } });
        expect(Number(lngInput.value)).toBeGreaterThanOrEqual(-180);
      });

      it('handles coordinate precision correctly', async () => {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Find coordinate inputs by accessible label instead of step attribute
        const latitudeInput = screen.queryByLabelText(/latitude/i) as HTMLInputElement | null;
        const longitudeInput = screen.queryByLabelText(/longitude/i) as HTMLInputElement | null;

        // Use the first coordinate input found (latitude or longitude)
        const input = latitudeInput ?? longitudeInput;
        expect(input).toBeTruthy();

        // Assert step property is appropriate for coordinates
        if (input) {
          const stepValue = parseFloat(input.step);
          expect(stepValue).toBeGreaterThanOrEqual(0.001);
        }

        if (input) {
          // Test high precision values
          await fireEvent.change(input, { target: { value: '40.712' } });
          expect(input.value).toBe('40.712');

          // Test very high precision (should maintain or round appropriately)
          await fireEvent.change(input, { target: { value: '40.7127816' } });
          // Should maintain reasonable precision - verify numeric value is rounded to 3 decimal places
          const numericValue = Number(input.value);
          expect(numericValue).toBeCloseTo(40.713, 3);
        }
      });
    });

    describe('BirdNET Settings - Threshold and Sensitivity', () => {
      it('validates sensitivity range (0.5 to 1.5)', async () => {
        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        render(MainSettingsPage.default);

        // Update settings to test boundaries
        settingsActions.updateSection('birdnet', {
          sensitivity: 0.4, // Below minimum
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          // Assert sensitivity is defined before checking bounds
          expect(settings?.sensitivity).toBeDefined();
          expect(settings.sensitivity).toBeGreaterThanOrEqual(0.5);
          expect(settings.sensitivity).toBeLessThanOrEqual(1.5);
        });
      });

      it('validates threshold range (0 to 1)', async () => {
        settingsActions.updateSection('birdnet', {
          threshold: -0.1, // Below minimum
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          // Assert threshold is defined before checking bounds
          expect(settings?.threshold).toBeDefined();
          expect(settings.threshold).toBeGreaterThanOrEqual(0);
          expect(settings.threshold).toBeLessThanOrEqual(1);
        });

        settingsActions.updateSection('birdnet', {
          threshold: 1.5, // Above maximum
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          // Assert threshold is defined before checking bounds
          expect(settings?.threshold).toBeDefined();
          expect(settings.threshold).toBeLessThanOrEqual(1);
        });
      });

      it('validates overlap percentage (0 to 100)', async () => {
        settingsActions.updateSection('birdnet', {
          overlap: -10, // Negative percentage
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          // Assert overlap is defined before checking bounds
          expect(settings?.overlap).toBeDefined();
          expect(settings.overlap).toBeGreaterThanOrEqual(0);
        });

        settingsActions.updateSection('birdnet', {
          overlap: 150, // Over 100%
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          // Assert overlap is defined before checking bounds
          expect(settings?.overlap).toBeDefined();
          expect(settings.overlap).toBeLessThanOrEqual(100);
        });
      });
    });
  });

  describe('String Input Validation', () => {
    it('handles special characters in OAuth client IDs', async () => {
      settingsActions.updateSection('security', {
        googleAuth: {
          enabled: true,
          clientId: '<script>alert("xss")</script>',
          clientSecret: 'secret',
        },
      } as any);

      const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
      const { component } = render(SecuritySettingsPage.default);

      // Should render without executing scripts
      expect(component).toBeTruthy();

      // Verify that the malicious input is rendered as text, not executed as HTML
      const scriptString = '<script>alert("xss")</script>';
      try {
        // Check if the raw script string appears as a display value (properly escaped)
        screen.getByDisplayValue(scriptString);
      } catch {
        // If not found by display value, check that it's visible as text content
        expect(screen.queryByText(scriptString)).toBeInTheDocument();
      }

      // Verify using DOM-based check instead of regex on innerHTML
      const scriptElements = document.body.querySelectorAll('script');
      const maliciousScripts = Array.from(scriptElements).filter(script => {
        const content = script.textContent ?? '';
        return content.includes('alert("xss")') || content.includes("alert('xss')");
      });
      expect(maliciousScripts.length).toBe(0);
    });
  });

  describe('Security Input Validation', () => {
    it('prevents XSS attacks in text inputs', async () => {
      const xssPayloads = [
        '<img src=x onerror=alert(1)>',
        '<svg onload=alert(1)>',
        'javascript:alert(1)',
        '<iframe src=javascript:alert(1)>',
      ];

      for (const payload of xssPayloads) {
        settingsActions.updateSection('realtime', {
          name: payload,
        } as any);

        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        const { component } = render(MainSettingsPage.default);

        expect(component).toBeTruthy();

        // Verify payload is escaped, not executed - use DOM-based check
        const container = document.body;
        const imgElements = container.querySelectorAll('img[onerror]');
        const svgElements = container.querySelectorAll('svg[onload]');
        const iframeElements = container.querySelectorAll('iframe[src*="javascript"]');

        expect(imgElements.length).toBe(0);
        expect(svgElements.length).toBe(0);
        expect(iframeElements.length).toBe(0);

        cleanup();
      }
    });

    it('handles SQL injection attempts in database fields', async () => {
      const sqlPayloads = [
        "'; DROP TABLE users; --",
        "1' OR '1'='1",
        "admin'--",
        "' UNION SELECT * FROM passwords --",
      ];

      for (const payload of sqlPayloads) {
        settingsActions.updateSection('birdnet', {
          database: {
            enabled: true,
            url: payload,
          },
        } as any);

        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        const { component } = render(MainSettingsPage.default);

        expect(component).toBeTruthy();

        // Verify the payload is treated as plain text
        const input = screen.queryByDisplayValue(payload);
        if (input) {
          expect(input).toBeInTheDocument();
          // Ensure it's in an input field, not executed
          expect(input.tagName).toBe('INPUT');
        }

        cleanup();
      }
    });

    it('sanitizes path traversal attempts', async () => {
      const pathPayloads = [
        '../../../etc/passwd',
        '..\\..\\..\\windows\\system32',
        'file:///etc/passwd',
        '\\\\server\\share\\sensitive',
      ];

      for (const payload of pathPayloads) {
        settingsActions.updateSection('birdnet', {
          modelPath: payload,
        } as any);

        const MainSettingsPage = await import('./MainSettingsPage.svelte');
        const { component } = render(MainSettingsPage.default);

        expect(component).toBeTruthy();

        // Verify path is displayed as-is, not resolved
        const input = screen.queryByDisplayValue(payload);
        if (input) {
          expect(input).toBeInTheDocument();
          expect(input.tagName).toBe('INPUT');
        }

        cleanup();
      }
    });
  });
});

describe('Array Input Validation', () => {
  it('validates maximum array length for species lists', async () => {
    // Create a very large species list
    const largeList = Array.from({ length: 1000 }, (_, i) => `Species_${i}`);

    settingsActions.updateSection('realtime', {
      species: {
        include: largeList,
        exclude: [],
        config: {},
      },
    });

    const SpeciesSettingsPage = await import('./SpeciesSettingsPage.svelte');
    const { component } = render(SpeciesSettingsPage.default);

    expect(component).toBeTruthy();

    // Check if there's any limit enforcement or performance issue
    const settings = get(settingsStore);
    const speciesList = (settings.formData as any)?.realtime?.species?.include;

    // Assert species list exists and is an array
    expect(speciesList).toBeDefined();
    expect(Array.isArray(speciesList)).toBe(true);
    expect(speciesList.length).toBeGreaterThan(0);

    // Test that the system handles large lists correctly
    // Either all 1000 entries are preserved OR there's a reasonable cap (>= 500)
    const originalLength = largeList.length; // 1000 entries
    const isWithinReasonableRange =
      speciesList.length === originalLength || speciesList.length >= 500;

    expect(isWithinReasonableRange).toBe(true);
  });
});

describe('Cross-Field Dependencies', () => {
  it('validates OAuth settings dependencies', async () => {
    // First enable OAuth in the store before rendering
    settingsActions.updateSection('security', {
      googleAuth: {
        enabled: true,
        clientId: 'test-client-id',
        clientSecret: 'test-client-secret',
      },
    } as any);

    const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
    const { component } = render(SecuritySettingsPage.default);

    // Component should render without errors with OAuth enabled
    expect(component).toBeTruthy();

    // When OAuth is enabled with credentials, the settings should be properly handled
    // Check that the security settings page rendered correctly
    await waitFor(() => {
      // Verify that the security page has rendered
      const securityCards = screen.queryAllByTestId('settings-card');
      expect(securityCards.length).toBeGreaterThan(0);
    });

    // Test validates that OAuth settings with dependencies (enabled + credentials)
    // are handled correctly without errors
    const settings = get(settingsStore);
    expect((settings.formData as any)?.security?.googleAuth?.enabled).toBe(true);
    expect((settings.formData as any)?.security?.googleAuth?.clientId).toBe('test-client-id');
  });

  it('validates MQTT broker dependencies', async () => {
    const IntegrationSettingsPage = await import('./IntegrationSettingsPage.svelte');
    const { component } = render(IntegrationSettingsPage.default);

    // Enable MQTT without broker - first update the store
    settingsActions.updateSection('realtime', {
      mqtt: {
        enabled: true,
        broker: '',
        port: 1883,
        topic: 'birdnet',
        username: '',
        password: '',
        tls: { enabled: false, skipVerify: false },
        retain: false,
      },
    } as any);

    // Wait for Svelte to update the DOM after store change
    await new Promise(resolve => setTimeout(resolve, 0));

    // Verify that the component is rendered and MQTT is enabled in the store
    expect(component).toBeTruthy();

    // When MQTT is enabled, broker and topic input fields should be visible
    // Use getByTestId or getByLabelText instead of getElementById for better test resilience
    const brokerInput = screen.queryByLabelText(/broker/i) ?? screen.queryByTestId('mqtt-broker');
    expect(brokerInput).toBeInTheDocument();

    const topicInput = screen.queryByLabelText(/topic/i) ?? screen.queryByTestId('mqtt-topic');
    expect(topicInput).toBeInTheDocument();
  });

  it('validates species configuration dependencies', async () => {
    const SpeciesSettingsPage = await import('./SpeciesSettingsPage.svelte');
    render(SpeciesSettingsPage.default);

    // Add configuration with action but no command
    settingsActions.updateSection('realtime', {
      species: {
        include: [],
        exclude: [],
        config: {
          TestBird: {
            threshold: 0.5,
            interval: 10,
            actions: [
              {
                type: 'ExecuteCommand',
                command: '', // Empty command
                parameters: ['param1'],
                executeDefaults: true,
              },
            ],
          },
        },
      },
    });

    // Should handle or validate empty command
    const settings = get(settingsStore);

    const config = (settings.formData as any)?.realtime?.species?.config?.TestBird;

    // Test invalid command handling - actions with empty commands should be filtered out
    expect(config?.actions).toBeDefined();
    expect(Array.isArray(config.actions)).toBe(true);

    // INTENTIONAL: Using conditional check because we're testing two valid outcomes:
    // 1. Invalid actions were filtered out (length = 0)
    // 2. Actions remain but have been sanitized (length > 0)
    // Both are correct behaviors depending on implementation.
    if (config.actions.length > 0) {
      const action = config.actions[0];
      expect(typeof action.command).toBe('string');
      expect(action.command.trim().length).toBeGreaterThan(0);
    }
  });
});

describe('Accessibility', () => {
  it('SecuritySettingsPage should have no accessibility violations', async () => {
    const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
    const { container } = render(SecuritySettingsPage.default);

    await waitFor(() => {
      // Wait for component to fully render
      expect(container.firstChild).toBeInTheDocument();
    });

    // Run axe-core accessibility tests
    await expectNoA11yViolations(container, A11Y_CONFIGS.forms);
  });
});

describe('Data Type Coercion', () => {
  it('coerces string numbers to numeric values', async () => {
    settingsActions.updateSection('birdnet', {
      sensitivity: '1.2' as any, // String instead of number

      threshold: '0.8' as any,

      overlap: '50' as any,
    });

    await waitFor(() => {
      const settings = get(birdnetSettings);

      // Should coerce to numbers
      // Settings from get() always exist but properties may be undefined in tests
      expect(typeof settings.sensitivity).toBe('number');
      expect(typeof settings.threshold).toBe('number');
      expect(typeof settings.overlap).toBe('number');
    });
  });

  it('coerces numeric strings to booleans appropriately', async () => {
    settingsActions.updateSection('security', {
      basicAuth: {
        enabled: '1' as any, // String "1" instead of boolean
      },
      autoTls: '0' as any, // String "0" instead of boolean (correct structure)
    } as any);

    await waitFor(() => {
      const settings = get(securitySettings);

      // Should handle string to boolean conversion
      // Settings from get() always exist but nested properties may be undefined

      expect(typeof (settings as any).basicAuth?.enabled).toBe('boolean');

      expect(typeof (settings as any).autoTls).toBe('boolean');
    });
  });

  it('handles null/undefined to default value conversion', async () => {
    settingsActions.updateSection('birdnet', {
      sensitivity: null,
      threshold: undefined,
      overlap: null,
    } as any);

    await waitFor(() => {
      const settings = get(birdnetSettings);

      // Should convert to default values
      // Settings from get() always exist but individual properties may be undefined in tests
      expect(settings.sensitivity).toBeDefined();
      expect(settings.threshold).toBeDefined();
      expect(settings.overlap).toBeDefined();

      // Should be valid numbers
      expect(typeof settings.sensitivity).toBe('number');
      expect(typeof settings.threshold).toBe('number');
      expect(typeof settings.overlap).toBe('number');
    });
  });
});

describe('Extreme Values', () => {
  it('handles Infinity and NaN in numeric fields', async () => {
    settingsActions.updateSection('birdnet', {
      sensitivity: Infinity as any,
      threshold: NaN as any,
      overlap: -Infinity as any,
    });

    await waitFor(() => {
      const settings = get(birdnetSettings);

      // Should handle extreme values gracefully with proper range constraints
      // Sensitivity should be finite and within valid range (0.5 to 1.5)
      expect(isFinite(settings.sensitivity)).toBe(true);
      expect(settings.sensitivity).toBeGreaterThanOrEqual(0.5);
      expect(settings.sensitivity).toBeLessThanOrEqual(1.5);

      // Threshold should be finite, not NaN, and within valid range (0 to 1)
      expect(isNaN(settings.threshold)).toBe(false);
      expect(isFinite(settings.threshold)).toBe(true);
      expect(settings.threshold).toBeGreaterThanOrEqual(0);
      expect(settings.threshold).toBeLessThanOrEqual(1);

      // Overlap should be finite and within valid range (0 to 100)
      expect(isFinite(settings.overlap)).toBe(true);
      expect(settings.overlap).toBeGreaterThanOrEqual(0);
      expect(settings.overlap).toBeLessThanOrEqual(100);
    });
  });

  it('handles very large numbers', async () => {
    settingsActions.updateSection('realtime', {
      audio: {
        captureDuration: Number.MAX_SAFE_INTEGER,
        bufferSize: Number.MAX_VALUE,
      } as any,
    });

    await waitFor(() => {
      const settings = get(audioSettings);

      // Should constrain to reasonable values for audio settings
      // Assert properties are defined before checking bounds
      expect((settings as any).captureDuration).toBeDefined();
      expect((settings as any).bufferSize).toBeDefined();

      // Capture duration should be constrained to a reasonable maximum (e.g., 1 hour = 3600 seconds)
      expect((settings as any).captureDuration).toBeLessThanOrEqual(3600);
      // Buffer size should be constrained to reasonable memory limits (e.g., 64MB = 67108864 bytes)
      expect((settings as any).bufferSize).toBeLessThanOrEqual(67108864);
    });
  });

  it('handles very small positive numbers', async () => {
    settingsActions.updateSection('birdnet', {
      threshold: Number.MIN_VALUE, // Smallest positive number
      sensitivity: Number.EPSILON, // Smallest difference
    } as any);

    await waitFor(() => {
      const settings = get(birdnetSettings);

      // Should handle or round appropriately
      expect(settings.threshold).toBeGreaterThanOrEqual(0);
      expect(settings.sensitivity).toBeGreaterThan(0);
    });
  });
});

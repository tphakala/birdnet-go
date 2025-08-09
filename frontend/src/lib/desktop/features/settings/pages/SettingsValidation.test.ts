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
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
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

        const latitudeInputs = screen
          .queryAllByRole('spinbutton')
          .filter(input => input.getAttribute('id')?.includes('latitude'));

        // Ensure latitude input exists before proceeding with tests
        expect(latitudeInputs.length).toBeGreaterThan(0);

        const latInput = latitudeInputs[0] as HTMLInputElement;

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

        const longitudeInputs = screen
          .queryAllByRole('spinbutton')
          .filter(input => input.getAttribute('id')?.includes('longitude'));

        // Ensure longitude input exists before proceeding with tests
        expect(longitudeInputs.length).toBeGreaterThan(0);

        const lngInput = longitudeInputs[0] as HTMLInputElement;

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

        const coordInputs = screen
          .queryAllByRole('spinbutton')
          .filter(input => input.getAttribute('step') === '0.000001');

        // Ensure coordinate input with step exists before proceeding with tests
        expect(coordInputs.length).toBeGreaterThan(0);

        const input = coordInputs[0] as HTMLInputElement;

        // Test high precision values
        await fireEvent.change(input, { target: { value: '40.7127816' } });
        expect(input.value).toBe('40.7127816');

        // Test very high precision (should maintain or round appropriately)
        await fireEvent.change(input, { target: { value: '40.71278161234567890' } });
        // Should maintain reasonable precision - verify numeric value is rounded to 6 decimal places
        const numericValue = Number(input.value);
        expect(numericValue).toBeCloseTo(40.712782, 6);
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
          // Should constrain to valid range

          if (settings?.sensitivity !== undefined) {
            expect(settings.sensitivity).toBeGreaterThanOrEqual(0.5);
            expect(settings.sensitivity).toBeLessThanOrEqual(1.5);
          }
        });
      });

      it('validates threshold range (0 to 1)', async () => {
        settingsActions.updateSection('birdnet', {
          threshold: -0.1, // Below minimum
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          if (settings?.threshold !== undefined) {
            expect(settings.threshold).toBeGreaterThanOrEqual(0);
            expect(settings.threshold).toBeLessThanOrEqual(1);
          }
        });

        settingsActions.updateSection('birdnet', {
          threshold: 1.5, // Above maximum
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          if (settings?.threshold !== undefined) {
            expect(settings.threshold).toBeLessThanOrEqual(1);
          }
        });
      });

      it('validates overlap percentage (0 to 100)', async () => {
        settingsActions.updateSection('birdnet', {
          overlap: -10, // Negative percentage
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          if (settings?.overlap !== undefined) {
            expect(settings.overlap).toBeGreaterThanOrEqual(0);
          }
        });

        settingsActions.updateSection('birdnet', {
          overlap: 150, // Over 100%
        } as any);

        await waitFor(() => {
          const settings = get(birdnetSettings);

          if (settings?.overlap !== undefined) {
            expect(settings.overlap).toBeLessThanOrEqual(100);
          }
        });
      });
    });

    describe('Audio Settings - Sample Rate and Buffer', () => {
      it('validates sample rate options', async () => {
        const AudioSettingsPage = await import('./AudioSettingsPage.svelte');
        render(AudioSettingsPage.default);

        // Query by accessible label text instead of fragile ID filtering
        const sampleRateSelects = screen.queryAllByLabelText(/sample rate/i);

        // Ensure sample rate select exists before proceeding with tests
        expect(sampleRateSelects.length).toBeGreaterThan(0);

        const select = sampleRateSelects[0] as HTMLSelectElement;

        // Check valid options
        const validRates = [16000, 22050, 24000, 44100, 48000];
        const options = Array.from(select.options).map(opt => Number(opt.value));

        options.forEach(rate => {
          if (!isNaN(rate) && rate > 0) {
            expect(validRates).toContain(rate);
          }
        });
      });

      it('validates capture duration limits', async () => {
        settingsActions.updateSection('realtime', {
          audio: {
            captureDuration: -1, // Negative duration
          } as any,
        });

        await waitFor(() => {
          const settings = get(audioSettings);

          if ((settings as any)?.captureDuration !== undefined) {
            // Should be corrected to minimum valid value (1 second)
            expect((settings as any).captureDuration).toBeGreaterThanOrEqual(1);
          }
        });

        settingsActions.updateSection('realtime', {
          audio: {
            captureDuration: 3601, // Over 1 hour
          } as any,
        });

        await waitFor(() => {
          const settings = get(audioSettings);

          if ((settings as any)?.captureDuration !== undefined) {
            // Should have reasonable upper limit

            expect((settings as any).captureDuration).toBeLessThanOrEqual(3600);
          }
        });
      });
    });
  });

  describe('String Input Validation', () => {
    describe('Security Settings - Password Requirements', () => {
      it('validates password minimum length', async () => {
        const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
        render(SecuritySettingsPage.default);

        const passwordInputs = screen.queryAllByLabelText(/password/i);

        // Ensure password input exists before proceeding with tests
        expect(passwordInputs.length).toBeGreaterThan(0);

        const pwdInput = passwordInputs[0] as HTMLInputElement;

        // Test too short password
        await fireEvent.change(pwdInput, { target: { value: '123' } });
        await fireEvent.blur(pwdInput);

        // Check for validation failure - either aria-invalid or error message present
        const isInvalid =
          pwdInput.getAttribute('aria-invalid') === 'true' ||
          pwdInput.classList.contains('input-error') ||
          screen.queryByText(/min(imum)?\s*(length|characters)/i) !== null;

        expect(isInvalid).toBe(true);
      });

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

        // Verify that the document body HTML doesn't contain executable script tags with malicious content
        const bodyHTML = document.body.innerHTML;
        const executableScriptRegex =
          /<script[^>]*>[\s\S]*alert\s*\(\s*["']xss["']\s*\)[\s\S]*<\/script>/i;
        expect(bodyHTML).not.toMatch(executableScriptRegex);
      });

      it('validates URL format for OAuth redirect URIs', async () => {
        const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
        render(SecuritySettingsPage.default);

        // Query by accessible label instead of fragile placeholder text
        const uriInputs = screen.queryAllByLabelText(/redirect.*uri/i);

        // Ensure redirect URI input exists before proceeding with tests
        expect(uriInputs.length).toBeGreaterThan(0);

        const uriInput = uriInputs[0] as HTMLInputElement;

        // Test valid URLs
        await fireEvent.change(uriInput, {
          target: { value: 'https://example.com/callback' },
        });
        expect(uriInput.value).toBe('https://example.com/callback');

        // Test invalid URLs - should show validation error via aria-invalid
        await fireEvent.change(uriInput, {
          target: { value: 'not-a-url' },
        });
        await fireEvent.blur(uriInput);

        // Explicitly check for validation failure via aria-invalid attribute
        expect(uriInput.getAttribute('aria-invalid')).toBe('true');
      });
    });

    describe('Network Settings - IP and Port Validation', () => {
      it('validates CIDR subnet format', async () => {
        const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
        render(SecuritySettingsPage.default);

        const subnetInputs = screen.queryAllByPlaceholderText(/CIDR/i);

        // Ensure CIDR input exists before proceeding with tests
        expect(subnetInputs.length).toBeGreaterThan(0);

        const input = subnetInputs[0] as HTMLInputElement;

        // Valid CIDR formats
        const validCIDRs = [
          '192.168.1.0/24',
          '10.0.0.0/8',
          '172.16.0.0/16',
          '::1/128',
          'fe80::/10',
        ];

        for (const cidr of validCIDRs) {
          await fireEvent.change(input, { target: { value: cidr } });
          await fireEvent.blur(input);

          // Value should remain exactly the same as entered
          expect(input.value).toBe(cidr);

          // Input should not have invalid state or error class
          expect(input.getAttribute('aria-invalid')).not.toBe('true');
          expect(input.classList.contains('input-error')).toBe(false);
        }

        // Invalid CIDR formats
        const invalidCIDRs = [
          '192.168.1.0/33', // Invalid mask
          '256.256.256.256/24', // Invalid IP
          '192.168.1.0', // Missing mask
          'not-an-ip/24',
        ];

        for (const cidr of invalidCIDRs) {
          await fireEvent.change(input, { target: { value: cidr } });
          await fireEvent.blur(input);

          // Check for validation error - either aria-invalid or error message
          const isInvalid =
            input.getAttribute('aria-invalid') === 'true' ||
            screen.queryByText(/invalid.*cidr|cidr.*invalid/i) !== null;

          expect(isInvalid).toBe(true);
        }
      });

      it('validates port number range (1-65535)', async () => {
        const IntegrationSettingsPage = await import('./IntegrationSettingsPage.svelte');
        render(IntegrationSettingsPage.default);

        const portInputs = screen
          .queryAllByRole('spinbutton')
          .filter(input => input.getAttribute('placeholder')?.includes('port'));

        if (portInputs.length > 0) {
          const portInput = portInputs[0] as HTMLInputElement;

          // Valid ports
          await fireEvent.change(portInput, { target: { value: '8080' } });
          expect(portInput.value).toBe('8080');

          await fireEvent.change(portInput, { target: { value: '1' } });
          expect(portInput.value).toBe('1');

          await fireEvent.change(portInput, { target: { value: '65535' } });
          expect(portInput.value).toBe('65535');

          // Invalid ports should be corrected to valid values
          await fireEvent.change(portInput, { target: { value: '0' } });
          expect(Number(portInput.value)).toBe(1); // Should be corrected to minimum valid port

          await fireEvent.change(portInput, { target: { value: '65536' } });
          expect(Number(portInput.value)).toBe(65535); // Should be corrected to maximum valid port

          await fireEvent.change(portInput, { target: { value: '-1' } });
          expect(Number(portInput.value)).toBe(1); // Should be corrected to minimum valid port
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

      // Should handle large lists appropriately
      expect(Array.isArray(speciesList)).toBe(true);

      // Assert either all entries are retained or there's a reasonable maximum cap
      const originalLength = largeList.length; // 1000 entries
      expect(speciesList.length).toBeGreaterThan(0);

      if (speciesList.length < originalLength) {
        // If there's a cap, it should be a reasonable maximum (e.g., 500 or similar)
        expect(speciesList.length).toBeGreaterThanOrEqual(500);
      } else {
        // If no cap, all original entries should be preserved
        expect(speciesList.length).toBe(originalLength);
      }
    });

    it('prevents duplicate entries in species lists', async () => {
      settingsActions.updateSection('realtime', {
        species: {
          include: ['Robin'],
          exclude: [],
          config: {},
        },
      });

      const SpeciesSettingsPage = await import('./SpeciesSettingsPage.svelte');
      render(SpeciesSettingsPage.default);

      // Try to add duplicate - elements must exist for test to be valid
      const addInput = screen.getByPlaceholderText(/add species to include/i);
      const addButton = screen.getByRole('button', { name: /add/i });

      await fireEvent.change(addInput, { target: { value: 'Robin' } });
      await fireEvent.click(addButton);

      // Check that duplicate wasn't added
      const settings = get(settingsStore);

      const includeList = (settings.formData as any)?.realtime?.species?.include;
      const robinCount = includeList?.filter((s: string) => s === 'Robin').length ?? 0;

      expect(robinCount).toBeLessThanOrEqual(1);
    });

    it('validates subnet array constraints', async () => {
      const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
      render(SecuritySettingsPage.default);

      // Try to add multiple subnets up to limit
      const maxSubnets = 5; // Based on SubnetInput maxItems prop
      const subnets = Array.from({ length: maxSubnets + 1 }, (_, i) => `192.168.${i}.0/24`);

      settingsActions.updateSection('security', {
        allowSubnetBypass: {
          enabled: true,
          subnets: subnets,
        },
      } as any);

      await waitFor(() => {
        const settings = get(securitySettings);
        const subnetList = (settings as any)?.allowSubnetBypass?.subnets;

        // Assert subnet list is defined and is an array
        expect(subnetList).toBeDefined();
        expect(Array.isArray(subnetList)).toBe(true);

        // Should enforce maximum items limit
        expect(subnetList.length).toBeLessThanOrEqual(maxSubnets);
      });
    });
  });

  describe('Cross-Field Dependencies', () => {
    it('validates OAuth settings dependencies', async () => {
      const SecuritySettingsPage = await import('./SecuritySettingsPage.svelte');
      render(SecuritySettingsPage.default);

      // Enable OAuth without credentials
      settingsActions.updateSection('security', {
        googleAuth: {
          enabled: true,
          clientId: '',
          clientSecret: '',
        },
      } as any);

      // Should show validation error or warning
      const warnings = screen.queryAllByText(/required/i);
      // OAuth should require credentials when enabled - expect at least one warning
      expect(warnings.length).toBeGreaterThan(0);
    });

    it('validates MQTT broker dependencies', async () => {
      const IntegrationSettingsPage = await import('./IntegrationSettingsPage.svelte');
      render(IntegrationSettingsPage.default);

      // Enable MQTT without broker

      settingsActions.updateSection('realtime', {
        mqtt: {
          enabled: true,
          broker: '',
          port: 1883,
        },
      } as any);

      // Broker input must be present for MQTT settings
      const brokerInput = screen.getByPlaceholderText(/broker/i);

      // Input should indicate required state
      const isRequired =
        brokerInput.hasAttribute('required') ||
        brokerInput.getAttribute('aria-required') === 'true';

      expect(isRequired).toBe(true);
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
      if (config?.actions?.length > 0) {
        const action = config.actions[0];

        // If action still exists, command should be properly sanitized and non-empty
        expect(typeof action.command).toBe('string');
        expect(action.command.trim().length).toBeGreaterThan(0);
      } else {
        // If no actions, empty command action should have been filtered out
        expect(config?.actions).toHaveLength(0);
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
        autoTLS: {
          enabled: '0' as any, // String "0" instead of boolean
        },
      } as any);

      await waitFor(() => {
        const settings = get(securitySettings);

        // Should handle string to boolean conversion
        // Settings from get() always exist but nested properties may be undefined

        expect(typeof (settings as any).basicAuth?.enabled).toBe('boolean');

        expect(typeof (settings as any).autoTLS?.enabled).toBe('boolean');
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
        if (settings) {
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
        }
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
        // Settings from get() always exist but test properties may not exist
        if ((settings as any).captureDuration !== undefined) {
          // Capture duration should be constrained to a reasonable maximum (e.g., 1 hour = 3600 seconds)
          expect((settings as any).captureDuration).toBeLessThanOrEqual(3600);
        }
        if ((settings as any).bufferSize !== undefined) {
          // Buffer size should be constrained to reasonable memory limits (e.g., 64MB = 67108864 bytes)
          expect((settings as any).bufferSize).toBeLessThanOrEqual(67108864);
        }
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
        if (settings) {
          expect(settings.threshold).toBeGreaterThanOrEqual(0);
          expect(settings.sensitivity).toBeGreaterThan(0);
        }
      });
    });
  });
});

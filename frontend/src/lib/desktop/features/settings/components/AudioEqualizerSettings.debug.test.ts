import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';

// Mock all dependencies to focus on core logic
vi.mock('$lib/i18n', () => ({ t: vi.fn(key => key) }));
vi.mock('$lib/utils/security', () => ({
  // eslint-disable-next-line security/detect-object-injection -- safe test mock
  safeGet: vi.fn((obj, key) => obj?.[key]),
  // eslint-disable-next-line security/detect-object-injection -- safe test mock
  safeArrayAccess: vi.fn((arr, index) => arr?.[index]),
}));
vi.mock('$lib/utils/logger', () => ({ loggers: { settings: { error: vi.fn() } } }));
vi.mock('./FilterResponseGraph.svelte', () => ({ default: () => null }));

describe('AudioEqualizerSettings - Debug New Filter Creation', () => {
  const mockUpdateCallback = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();

    // Mock failing API to force fallback config
    global.fetch = vi.fn(() =>
      Promise.resolve({ ok: false } as unknown as Response)
    ) as typeof global.fetch;
    document.querySelector = vi.fn(() => ({
      getAttribute: () => 'csrf',
    })) as typeof document.querySelector;
  });

  it('should debug what happens when adding new HighPass filter', async () => {
    const consoleSpy = vi.spyOn(console, 'log').mockImplementation(() => {});

    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: { enabled: true, filters: [] },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for config to load
    await waitFor(() => {
      expect(
        screen.getByDisplayValue('settings.audio.audioFilters.selectFilterType')
      ).toBeInTheDocument();
    });

    // Log the current component state by checking what's actually rendered
    // eslint-disable-next-line no-console -- intentional debug logging
    console.log('=== DEBUGGING NEW FILTER CREATION ===');

    // Select HighPass filter type
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    );
    await fireEvent.change(filterTypeSelect, { target: { value: 'HighPass' } });

    // Wait a bit for the component to update
    await new Promise(resolve => setTimeout(resolve, 100));

    // Check if attenuation select appears and what its value is
    try {
      const attenuationSelects = screen.getAllByRole('combobox');
      // eslint-disable-next-line no-console -- intentional debug logging
      console.log('Found selects:', attenuationSelects.length);

      attenuationSelects.forEach((select, index) => {
        const htmlSelect = select as HTMLSelectElement;
        // eslint-disable-next-line no-console -- intentional debug logging
        console.log(`Select ${index}:`, {
          value: htmlSelect.value,
          options: Array.from(htmlSelect.options).map(opt => ({
            value: opt.value,
            text: opt.text,
            selected: opt.selected,
          })),
        });
      });
    } catch (e) {
      // eslint-disable-next-line no-console -- intentional debug logging
      console.log('No selects found or error:', e instanceof Error ? e.message : String(e));
    }

    // Try to find Add Filter button
    try {
      const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
      // eslint-disable-next-line no-console -- intentional debug logging
      console.log('Add button found, enabled:', !addButton.hasAttribute('disabled'));

      // Click it and see what gets called
      await fireEvent.click(addButton);

      // eslint-disable-next-line no-console -- intentional debug logging
      console.log('Update callback called with:', mockUpdateCallback.mock.calls);
    } catch (e) {
      // eslint-disable-next-line no-console -- intentional debug logging
      console.log('Add button not found:', e instanceof Error ? e.message : String(e));
    }

    consoleSpy.mockRestore();
  });

  it('should check the actual fallback config values', async () => {
    const consoleSpy = vi.spyOn(console, 'log').mockImplementation(() => {});

    // We'll temporarily add a console.log inside the component to see the config
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: { enabled: true, filters: [] },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(
        screen.getByDisplayValue('settings.audio.audioFilters.selectFilterType')
      ).toBeInTheDocument();
    });

    // eslint-disable-next-line no-console -- intentional debug logging
    console.log('=== CONFIG DEBUG ===');
    // eslint-disable-next-line no-console -- intentional debug logging
    console.log('Component rendered successfully, check console for config details');

    consoleSpy.mockRestore();
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';

// Mock dependencies
// Common mocks are now handled globally in src/test/setup.ts
// ($lib/i18n, $lib/utils/security, $lib/utils/logger)

// Mock FilterResponseGraph to avoid canvas issues
vi.mock('./FilterResponseGraph.svelte', () => {
  return {
    default: vi.fn(() => ({
      $set: vi.fn(),
      $destroy: vi.fn(),
      $on: vi.fn(),
    })),
  };
});

describe('AudioEqualizerSettings', () => {
  const mockUpdateCallback = vi.fn();

  const defaultEqualizerSettings = {
    enabled: true,
    filters: [],
  };

  beforeEach(() => {
    vi.clearAllMocks();

    // Mock fetch for filter config API
    global.fetch = vi.fn(() =>
      Promise.resolve({
        ok: false, // Force fallback config usage
        json: () => Promise.resolve({}),
      } as unknown as Response)
    ) as typeof global.fetch;

    // Mock CSRF token
    document.querySelector = vi.fn((selector: string): Element | null => {
      if (selector === 'meta[name="csrf-token"]') {
        return { getAttribute: () => 'mock-csrf-token' } as unknown as Element;
      }
      return null;
    }) as typeof document.querySelector;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('should render with empty filters initially', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    });

    // Should show "Select filter type" dropdown
    expect(screen.getByText('settings.audio.audioFilters.selectFilterType')).toBeInTheDocument();
  });

  it('should use 12dB (value=1) as default for new HighPass filter', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load config
    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.selectFilterType')).toBeInTheDocument();
    });

    // Select HighPass filter type
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    );
    await fireEvent.change(filterTypeSelect, { target: { value: 'HighPass' } });

    // Wait for parameter inputs to appear
    await waitFor(() => {
      const attenuationSelects = screen.getAllByDisplayValue('12dB');
      expect(attenuationSelects.length).toBeGreaterThan(0);
    });

    // Check that attenuation defaults to 12dB (value="1")
    const attenuationSelect = screen.getByDisplayValue('12dB');
    expect(attenuationSelect).toBeInTheDocument();
    expect((attenuationSelect as HTMLSelectElement).value).toBe('1');
  });

  it('should use 12dB (value=1) as default for new LowPass filter', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load config
    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.selectFilterType')).toBeInTheDocument();
    });

    // Select LowPass filter type
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    );
    await fireEvent.change(filterTypeSelect, { target: { value: 'LowPass' } });

    // Wait for parameter inputs to appear
    await waitFor(() => {
      const attenuationSelects = screen.getAllByDisplayValue('12dB');
      expect(attenuationSelects.length).toBeGreaterThan(0);
    });

    // Check that attenuation defaults to 12dB (value="1")
    const attenuationSelect = screen.getByDisplayValue('12dB');
    expect(attenuationSelect).toBeInTheDocument();
    expect((attenuationSelect as HTMLSelectElement).value).toBe('1');
  });

  it('should add new HighPass filter with passes=1 when Add Filter clicked', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load config
    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.selectFilterType')).toBeInTheDocument();
    });

    // Select HighPass filter type
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    );
    await fireEvent.change(filterTypeSelect, { target: { value: 'HighPass' } });

    // Wait for Add Filter button to be enabled
    await waitFor(() => {
      const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
      expect(addButton).not.toBeDisabled();
    });

    // Click Add Filter button
    const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
    await fireEvent.click(addButton);

    // Verify onUpdate was called with correct filter data
    expect(mockUpdateCallback).toHaveBeenCalledWith(
      expect.objectContaining({
        enabled: true,
        filters: expect.arrayContaining([
          expect.objectContaining({
            type: 'HighPass',
            frequency: 100, // Default frequency for HighPass
            passes: 1, // This should be 1 (12dB), not 0!
            q: 0.707, // Butterworth Q factor
          }),
        ]),
      })
    );
  });

  it('should display existing HighPass filter with correct attenuation value', async () => {
    const existingFilter = {
      type: 'HighPass' as const,
      frequency: 100,
      passes: 2, // 24dB attenuation
      q: 0.707,
    };

    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: {
          enabled: true,
          filters: [existingFilter],
        },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load
    await waitFor(() => {
      expect(screen.getByText('HighPass Filter')).toBeInTheDocument();
    });

    // Check that the attenuation select shows the correct value (24dB)
    const attenuationSelect = screen.getByDisplayValue('24dB');
    expect(attenuationSelect).toBeInTheDocument();
    expect((attenuationSelect as HTMLSelectElement).value).toBe('2');
  });

  it('should handle missing passes property gracefully', async () => {
    const filterWithoutPasses = {
      type: 'HighPass' as const,
      frequency: 100,
      // passes property missing - should default to 1 (12dB)
      q: 0.707,
    };

    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: {
          enabled: true,
          filters: [filterWithoutPasses],
        },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load
    await waitFor(() => {
      expect(screen.getByText('HighPass Filter')).toBeInTheDocument();
    });

    // Should show 12dB as default when passes is missing
    const attenuationSelect = screen.getByDisplayValue('12dB');
    expect(attenuationSelect).toBeInTheDocument();
    expect((attenuationSelect as HTMLSelectElement).value).toBe('1');
  });
});

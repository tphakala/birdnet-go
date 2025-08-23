import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';

// Mock all dependencies to focus on core logic
vi.mock('$lib/i18n', () => ({ t: vi.fn(key => key) }));
vi.mock('$lib/utils/security', () => ({
  // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
  safeGet: vi.fn((obj, key) => obj?.[key]),
  // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled data
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

  it('should create HighPass filter with expected controls and callback', async () => {
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

    // Select HighPass filter type
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    );
    await fireEvent.change(filterTypeSelect, { target: { value: 'HighPass' } });

    // Wait for the component to update
    await waitFor(() => {
      const attenuationSelects = screen.getAllByRole('combobox');
      expect(attenuationSelects.length).toBe(2); // Filter type + attenuation select
    });

    // Check that attenuation select appears with expected default value
    const attenuationSelects = screen.getAllByRole('combobox');
    const attenuationSelect = attenuationSelects.find(select => {
      const htmlSelect = select as HTMLSelectElement;
      return Array.from(htmlSelect.options).some(opt => opt.text.includes('dB'));
    }) as HTMLSelectElement;

    expect(attenuationSelect).toBeInTheDocument();
    expect(attenuationSelect.value).toBe('1'); // Default to 12dB (1 pass)

    // Verify available options
    const optionValues = Array.from(attenuationSelect.options).map(opt => opt.value);
    expect(optionValues).toEqual(expect.arrayContaining(['0', '1', '2', '3', '4']));

    // Check that Add Filter button is present and enabled
    const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
    expect(addButton).toBeInTheDocument();
    expect(addButton).toBeEnabled();

    // Click Add Filter button
    await fireEvent.click(addButton);

    // Assert the update callback was called with expected payload
    expect(mockUpdateCallback).toHaveBeenCalledTimes(1);
    expect(mockUpdateCallback).toHaveBeenCalledWith(
      expect.objectContaining({
        enabled: true,
        filters: expect.arrayContaining([
          expect.objectContaining({
            type: 'HighPass',
            frequency: 100, // Default frequency for HighPass
            passes: 1, // Default to 12dB
            q: 0.707, // Butterworth Q factor
          }),
        ]),
      })
    );
  });

  it('should use fallback config when API fails', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: { enabled: true, filters: [] },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load with fallback config
    await waitFor(() => {
      expect(
        screen.getByDisplayValue('settings.audio.audioFilters.selectFilterType')
      ).toBeInTheDocument();
    });

    // Verify fallback config is used by checking available filter types
    const filterTypeSelect = screen.getByDisplayValue(
      'settings.audio.audioFilters.selectFilterType'
    ) as HTMLSelectElement;

    const filterTypeOptions = Array.from(filterTypeSelect.options)
      .map(opt => opt.value)
      .filter(value => value !== ''); // Remove empty "select" option

    expect(filterTypeOptions).toEqual(expect.arrayContaining(['LowPass', 'HighPass']));

    // Select LowPass and verify fallback default values
    await fireEvent.change(filterTypeSelect, { target: { value: 'LowPass' } });

    await waitFor(() => {
      // Should show frequency input with default 15000 Hz
      const frequencyInput = screen.getByDisplayValue('15000');
      expect(frequencyInput).toBeInTheDocument();
    });

    // Select HighPass and verify different default frequency
    await fireEvent.change(filterTypeSelect, { target: { value: 'HighPass' } });

    await waitFor(() => {
      // Should show frequency input with default 100 Hz
      const frequencyInput = screen.getByDisplayValue('100');
      expect(frequencyInput).toBeInTheDocument();
    });

    // Verify onUpdate is not called on initial render
    expect(mockUpdateCallback).not.toHaveBeenCalled();

    // Verify form controls exist with expected fallback state
    expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    expect(screen.getByText('settings.audio.audioFilters.addFilter')).toBeInTheDocument();
  });
});

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';

// Mock additional dependencies specific to this test
vi.mock('./FilterResponseGraph.svelte', () => ({ default: () => null }));

// Mock the icon components to avoid SVG import issues in tests
vi.mock('$lib/desktop/components/ui/LowPassIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('$lib/desktop/components/ui/HighPassIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('$lib/desktop/components/ui/BandRejectIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));

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

  it('should render filter type dropdown with expected controls', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: { enabled: true, filters: [] },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for config to load - check for the filter type label
    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.newFilterType')).toBeInTheDocument();
    });

    // Check that Add Filter button is present and disabled (no filter type selected)
    const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
    expect(addButton).toBeInTheDocument();
    expect(addButton).toBeDisabled();

    // Verify form controls exist
    expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
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
      expect(screen.getByText('settings.audio.audioFilters.newFilterType')).toBeInTheDocument();
    });

    // Verify onUpdate is not called on initial render
    expect(mockUpdateCallback).not.toHaveBeenCalled();

    // Verify form controls exist with expected fallback state
    expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    expect(screen.getByText('settings.audio.audioFilters.addFilter')).toBeInTheDocument();
  });

  it('should display existing filter in the list', async () => {
    const existingFilter = {
      type: 'HighPass' as const,
      frequency: 100,
      passes: 1,
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
      expect(screen.getByText('HighPass')).toBeInTheDocument();
    });

    // Verify the filter type is displayed
    expect(screen.getByText('HighPass')).toBeInTheDocument();
  });
});

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import FilterSettingsPage from './FilterSettingsPage.svelte';
import type { SettingsFormData } from '$lib/stores/settings';

// Mock API module
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ species: [] }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

// Mock settings stores and actions
const mockSettingsStore = writable({
  isLoading: false,
  isSaving: false,
  error: null,
  activeSection: 'filters',
  originalData: {
    realtime: {
      privacyFilter: {
        enabled: true,
        confidence: 0.05,
        debug: false,
        vad: {
          enabled: false,
          threshold: 0.35,
          modelPath: '',
        },
      },
      dogBarkFilter: {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      },
      daylightFilter: {
        enabled: false,
        debug: false,
        offset: 0,
        species: [],
      },
    },
  } as unknown as SettingsFormData,
  formData: {
    realtime: {
      privacyFilter: {
        enabled: true,
        confidence: 0.05,
        debug: false,
        vad: {
          enabled: false,
          threshold: 0.35,
          modelPath: '',
        },
      },
      dogBarkFilter: {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      },
      daylightFilter: {
        enabled: false,
        debug: false,
        offset: 0,
        species: [],
      },
    },
  } as unknown as SettingsFormData,
});

const mockPrivacyFilterSettings = writable({
  enabled: true,
  confidence: 0.05,
  debug: false,
  vad: {
    enabled: false,
    threshold: 0.35,
    modelPath: '',
  },
});

const mockDogBarkFilterSettings = writable({
  enabled: false,
  confidence: 0.5,
  remember: 30,
  debug: false,
  species: [],
});

const mockDaylightFilterSettings = writable({
  enabled: false,
  debug: false,
  offset: 0,
  species: [],
});

const mockRealtimeSettings = writable({
  privacyFilter: {
    enabled: true,
    confidence: 0.05,
    debug: false,
    vad: {
      enabled: false,
      threshold: 0.35,
      modelPath: '',
    },
  },
});

const mockUpdateSection = vi.fn();

vi.mock('$lib/stores/settings', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/stores/settings')>();
  return {
    ...actual,
    settingsStore: {
      subscribe: (fn: (val: unknown) => void) => mockSettingsStore.subscribe(fn),
    },
    privacyFilterSettings: {
      subscribe: (fn: (val: unknown) => void) => mockPrivacyFilterSettings.subscribe(fn),
    },
    dogBarkFilterSettings: {
      subscribe: (fn: (val: unknown) => void) => mockDogBarkFilterSettings.subscribe(fn),
    },
    daylightFilterSettings: {
      subscribe: (fn: (val: unknown) => void) => mockDaylightFilterSettings.subscribe(fn),
    },
    realtimeSettings: {
      subscribe: (fn: (val: unknown) => void) => mockRealtimeSettings.subscribe(fn),
    },
    settingsActions: {
      ...actual.settingsActions,
      updateSection: (...args: unknown[]) => mockUpdateSection(...args),
    },
    hasUnsavedChanges: writable(false),
  };
});

describe('FilterSettingsPage - Privacy Guard & VAD Settings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockPrivacyFilterSettings.set({
      enabled: true,
      confidence: 0.05,
      debug: false,
      vad: {
        enabled: false,
        threshold: 0.35,
        modelPath: '',
      },
    });
  });

  it('renders Privacy Filtering section with master toggle and Privacy Guard card', async () => {
    const { getByText, getAllByText } = render(FilterSettingsPage);

    expect(getByText('settings.filters.privacyFiltering.title')).toBeInTheDocument();
    expect(getAllByText('settings.filters.privacyFiltering.enable').length).toBeGreaterThanOrEqual(
      1
    );
    expect(getByText('settings.filters.privacyFiltering.vadEnable')).toBeInTheDocument();
    expect(getByText('settings.filters.privacyFiltering.vadHelp')).toBeInTheDocument();
  });

  it('renders classifier confidence threshold when Privacy Guard is disabled', async () => {
    const { getByText, queryByText } = render(FilterSettingsPage);

    expect(getByText('settings.filters.privacyFiltering.confidenceLabel')).toBeInTheDocument();
    expect(getByText('settings.filters.privacyFiltering.confidenceHelp')).toBeInTheDocument();
    expect(
      queryByText('settings.filters.privacyFiltering.vadThresholdLabel')
    ).not.toBeInTheDocument();
  });

  it('renders VAD speech detection threshold when Privacy Guard is enabled', async () => {
    mockPrivacyFilterSettings.set({
      enabled: true,
      confidence: 0.05,
      debug: false,
      vad: {
        enabled: true,
        threshold: 0.35,
        modelPath: '',
      },
    });

    const { getByText, queryByText } = render(FilterSettingsPage);

    expect(getByText('settings.filters.privacyFiltering.vadThresholdLabel')).toBeInTheDocument();
    expect(getByText('settings.filters.privacyFiltering.vadThresholdHelp')).toBeInTheDocument();
    expect(
      queryByText('settings.filters.privacyFiltering.confidenceLabel')
    ).not.toBeInTheDocument();
  });

  it('calls updateSection when toggling Privacy Guard checkbox', async () => {
    const { container } = render(FilterSettingsPage);

    // Find the Privacy Guard checkbox (the second checkbox on the page)
    const checkboxes = container.querySelectorAll('input[type="checkbox"]');
    expect(checkboxes.length).toBeGreaterThanOrEqual(2);
    const vadCheckbox = checkboxes[1];

    await fireEvent.click(vadCheckbox);

    expect(mockUpdateSection).toHaveBeenCalledWith(
      'realtime',
      expect.objectContaining({
        privacyFilter: expect.objectContaining({
          vad: expect.objectContaining({
            enabled: true,
          }),
        }),
      })
    );
  });

  it('calls updateSection when updating VAD threshold', async () => {
    mockPrivacyFilterSettings.set({
      enabled: true,
      confidence: 0.05,
      debug: false,
      vad: {
        enabled: true,
        threshold: 0.35,
        modelPath: '',
      },
    });

    const { container } = render(FilterSettingsPage);

    const numberInput = container.querySelector('input[type="number"]');
    expect(numberInput).toBeInTheDocument();

    await fireEvent.input(numberInput as HTMLInputElement, { target: { value: '0.40' } });
    await fireEvent.change(numberInput as HTMLInputElement, { target: { value: '0.40' } });

    await waitFor(() => {
      expect(mockUpdateSection).toHaveBeenCalledWith(
        'realtime',
        expect.objectContaining({
          privacyFilter: expect.objectContaining({
            vad: expect.objectContaining({
              threshold: 0.4,
            }),
          }),
        })
      );
    });
  });
});

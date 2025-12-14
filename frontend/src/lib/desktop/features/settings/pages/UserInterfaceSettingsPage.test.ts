import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  screen,
  createI18nMock,
  createComponentTestFactory,
  mockDOMAPIs,
  waitFor,
} from '../../../../../test/render-helpers';
import { within } from '@testing-library/dom';
import { get } from 'svelte/store';
import UserInterfaceSettingsPage from './UserInterfaceSettingsPage.svelte';
import { settingsStore } from '$lib/stores/settings';

// Mock API module
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
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

// Mock toast actions
vi.mock('$lib/stores/toast', () => ({
  toastActions: {
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
  },
}));

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'settings.main.sections.userInterface.interface.title': 'Interface Settings',
    'settings.main.sections.userInterface.interface.description':
      'Choose your preferred language and interface version',
    'settings.main.sections.userInterface.interface.locale.label': 'Language',
    'settings.main.sections.userInterface.interface.locale.helpText':
      'Select your preferred language',
    'settings.main.sections.userInterface.interface.newUI.label': 'Use New User Interface',
    'settings.main.sections.userInterface.interface.newUI.helpText':
      'Enable redirect from old HTMX UI to new Svelte UI',
    'settings.main.sections.userInterface.dashboard.title': 'Dashboard Display',
    'settings.main.sections.userInterface.dashboard.description':
      'Configure how information is displayed on the dashboard',
    'settings.main.sections.userInterface.dashboard.displaySettings.title': 'Display Settings',
    'settings.main.sections.userInterface.dashboard.displaySettings.description':
      'Configure general dashboard display options',
    'settings.main.sections.userInterface.dashboard.birdImages.title': 'Bird Images',
    'settings.main.sections.userInterface.dashboard.birdImages.description':
      'Configure bird species thumbnail images',
    'settings.main.sections.userInterface.dashboard.spectrogramGeneration.title':
      'Spectrogram Generation',
    'settings.main.sections.userInterface.dashboard.spectrogramGeneration.description':
      'Configure when and how spectrograms are generated',
    'settings.main.sections.userInterface.dashboard.summaryLimit.label': 'Summary Limit',
    'settings.main.sections.userInterface.dashboard.summaryLimit.helpText':
      'Maximum number of items to show in summaries',
    'settings.main.sections.userInterface.dashboard.thumbnails.title': 'Thumbnails',
    'settings.main.sections.userInterface.dashboard.thumbnails.summary.label': 'Show in Summary',
    'settings.main.sections.userInterface.dashboard.thumbnails.summary.helpText':
      'Display thumbnails in the summary view',
    'settings.main.sections.userInterface.dashboard.thumbnails.recent.label': 'Show in Recent',
    'settings.main.sections.userInterface.dashboard.thumbnails.recent.helpText':
      'Display thumbnails in the recent detections view',
    'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.label':
      'Image Provider',
    'settings.main.sections.userInterface.dashboard.thumbnails.imageProvider.helpText':
      'Source for bird images',
    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.label':
      'Fallback Policy',
    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.helpText':
      'How to handle missing images',
    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.all':
      'Try all providers',
    'settings.main.sections.userInterface.dashboard.thumbnails.fallbackPolicy.options.none':
      'No fallback',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.label': 'Mode',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.label': 'Auto',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.auto.helpText':
      'Automatically generate spectrograms',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.label': 'Pre-render',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.prerender.helpText':
      'Pre-render spectrograms',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.label':
      'On Demand',
    'settings.main.sections.userInterface.dashboard.spectrogram.mode.userRequested.helpText':
      'Generate spectrograms on user request',
    'settings.main.errors.providersLoadFailed': 'Failed to load image providers',
    'settings.card.changed': 'Changed',
    'settings.card.changedAriaLabel': 'Settings changed',
    'settings.tabs.navigation': 'Settings navigation',
    'settings.tabs.hasChanges': 'Tab has unsaved changes',
  }),
  getLocale: vi.fn(() => 'en'),
}));

// Mock i18n config - need to define LOCALES separately since it's imported directly
vi.mock('$lib/i18n/config', () => ({
  LOCALES: {
    en: { name: 'English', flag: '🇺🇸' },
    de: { name: 'Deutsch', flag: '🇩🇪' },
    es: { name: 'Español', flag: '🇪🇸' },
    fr: { name: 'Français', flag: '🇫🇷' },
    fi: { name: 'Suomi', flag: '🇫🇮' },
    pt: { name: 'Português', flag: '🇵🇹' },
  },
}));

// Mock settings store
vi.mock('$lib/stores/settings', async () => {
  const { writable, derived } = await import('svelte/store');

  const settingsStore = writable({
    formData: {},
    originalData: {},
    isLoading: false,
    isSaving: false,
    activeSection: 'userInterface',
    error: null,
  });

  const dashboardSettings = derived(settingsStore, $store => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return ($store.formData as any)?.realtime?.dashboard;
  });

  const hasUnsavedChanges = writable(false);

  // Mock updateSection to actually update the store
  const settingsActions = {
    updateSection: vi.fn((section: string, data: Record<string, unknown>) => {
      settingsStore.update(state => ({
        ...state,
        formData: {
          ...state.formData,

          [section]: {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any, security/detect-object-injection -- test mock
            ...(state.formData as any)?.[section],
            ...data,
          },
        },
      }));
    }),
  };

  const DEFAULT_SPECTROGRAM_SETTINGS = {
    enabled: false,
    preRender: 'none' as const,
  };

  return {
    settingsStore,
    dashboardSettings,
    hasUnsavedChanges,
    settingsActions,
    DEFAULT_SPECTROGRAM_SETTINGS,
  };
});

// Mock hasSettingsChanged
vi.mock('$lib/utils/settingsChanges', () => ({
  hasSettingsChanged: vi.fn(),
}));

// Set up DOM APIs
mockDOMAPIs();

// Create test factory
const testFactory = createComponentTestFactory(UserInterfaceSettingsPage);

// Escape special regex characters to safely use user input in RegExp
function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// Helper to switch to a specific tab
async function switchToTab(tabName: string) {
  // Input is escaped to prevent ReDoS, safe to use in RegExp
  const escapedName = escapeRegExp(tabName);
  // eslint-disable-next-line security/detect-non-literal-regexp -- input is sanitized by escapeRegExp
  const tab = screen.getByRole('tab', { name: new RegExp(escapedName, 'i') });
  tab.click();
  await waitFor(() => {
    expect(tab.getAttribute('aria-selected')).toBe('true');
  });
}

describe('UserInterfaceSettingsPage', () => {
  let mockApi: {
    get: ReturnType<typeof vi.fn>;
    post: ReturnType<typeof vi.fn>;
  };
  let mockHasSettingsChanged: ReturnType<typeof vi.fn>;
  let mockToastActions: {
    success: ReturnType<typeof vi.fn>;
    error: ReturnType<typeof vi.fn>;
    warning: ReturnType<typeof vi.fn>;
  };

  beforeEach(async () => {
    vi.clearAllMocks();

    // Clear localStorage to reset tab persistence state
    localStorage.clear();

    // Get mocked modules
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    mockApi = (await import('$lib/utils/api')).api as any;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    mockToastActions = (await import('$lib/stores/toast')).toastActions as any;
    const mocked = await vi.importMock<typeof import('$lib/utils/settingsChanges')>(
      '$lib/utils/settingsChanges'
    );
    mockHasSettingsChanged = mocked.hasSettingsChanged as ReturnType<typeof vi.fn>;

    // Default mock responses
    mockHasSettingsChanged.mockReturnValue(false);
    mockApi.get.mockResolvedValue({
      providers: [
        { value: 'wikimedia', display: 'Wikimedia' },
        { value: 'wikipedia', display: 'Wikipedia' },
      ],
    });

    // Initialize store with default settings
    const formData = {
      realtime: {
        dashboard: {
          thumbnails: {
            summary: true,
            recent: true,
            imageProvider: 'wikimedia',
            fallbackPolicy: 'all',
          },
          summaryLimit: 100,
          locale: 'en',
          newUI: false,
        },
      },
    };

    settingsStore.set({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      formData: formData as any,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      originalData: JSON.parse(JSON.stringify(formData)) as any,
      isLoading: false,
      isSaving: false,
      activeSection: 'userInterface',
      error: null,
    });
  });

  afterEach(() => {
    vi.clearAllTimers();
  });

  describe('Rendering', () => {
    it('renders tab navigation with both tabs', async () => {
      testFactory.render();

      await waitFor(() => {
        // Check that both tabs are present
        expect(screen.getByRole('tab', { name: /Interface Settings/i })).toBeInTheDocument();
        expect(screen.getByRole('tab', { name: /Dashboard Display/i })).toBeInTheDocument();
      });
    });

    it('renders interface settings controls on Interface tab', async () => {
      testFactory.render();

      // Interface tab is active by default
      await waitFor(() => {
        // Language selector
        expect(screen.getByLabelText('Language')).toBeInTheDocument();

        // New UI checkbox
        expect(screen.getByLabelText('Use New User Interface')).toBeInTheDocument();
      });
    });

    it('renders dashboard display controls on Dashboard tab', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        // Summary limit (in Display Settings section)
        expect(screen.getByLabelText('Summary Limit')).toBeInTheDocument();

        // Thumbnail checkboxes (in Bird Images section)
        expect(screen.getByLabelText('Show in Summary')).toBeInTheDocument();
        expect(screen.getByLabelText('Show in Recent')).toBeInTheDocument();

        // Image provider selector
        expect(screen.getByLabelText('Image Provider')).toBeInTheDocument();
      });
    });

    it('renders language selector label', async () => {
      testFactory.render();

      await waitFor(() => {
        // Check that the language label is rendered (SelectDropdown uses custom UI)
        expect(screen.getByText('Language')).toBeInTheDocument();
      });
    });
  });

  describe('Interface Settings', () => {
    it('renders interface settings controls', async () => {
      testFactory.render();

      await waitFor(() => {
        // Language label is rendered (SelectDropdown uses custom button-based UI)
        expect(screen.getByText('Language')).toBeInTheDocument();

        // New UI checkbox uses standard label
        expect(screen.getByLabelText('Use New User Interface')).toBeInTheDocument();
      });
    });

    it('updates newUI setting when checkbox is toggled', async () => {
      testFactory.render();

      await waitFor(() => {
        const checkbox = screen.getByLabelText('Use New User Interface') as HTMLInputElement;
        expect(checkbox).toBeInTheDocument();
      });

      const checkbox = screen.getByLabelText('Use New User Interface') as HTMLInputElement;
      expect(checkbox.checked).toBe(false);

      // Toggle checkbox
      checkbox.click();

      await waitFor(() => {
        const store = get(settingsStore);

        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        expect((store.formData as any)?.realtime?.dashboard?.newUI).toBe(true);
      });
    });
  });

  describe('Dashboard Display Settings', () => {
    it('updates summary limit', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const input = screen.getByLabelText('Summary Limit') as HTMLInputElement;
        expect(input).toBeInTheDocument();
      });

      const input = screen.getByLabelText('Summary Limit') as HTMLInputElement;
      expect(input.value).toBe('100');

      // Change value
      input.value = '50';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.dispatchEvent(new Event('change', { bubbles: true }));

      await waitFor(() => {
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        expect((store.formData as any)?.realtime?.dashboard?.summaryLimit).toBe(50);
      });
    });

    it('updates thumbnail settings', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const summaryCheckbox = screen.getByLabelText('Show in Summary') as HTMLInputElement;
        expect(summaryCheckbox).toBeInTheDocument();
      });

      const summaryCheckbox = screen.getByLabelText('Show in Summary') as HTMLInputElement;
      const recentCheckbox = screen.getByLabelText('Show in Recent') as HTMLInputElement;

      expect(summaryCheckbox.checked).toBe(true);
      expect(recentCheckbox.checked).toBe(true);

      // Toggle checkboxes
      summaryCheckbox.click();
      recentCheckbox.click();

      await waitFor(() => {
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const thumbnails = (store.formData as any)?.realtime?.dashboard?.thumbnails;
        expect(thumbnails?.summary).toBe(false);
        expect(thumbnails?.recent).toBe(false);
      });
    });

    it('updates image provider selection', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const select = screen.getByLabelText('Image Provider') as HTMLSelectElement;
        expect(select).toBeInTheDocument();
      });

      const select = screen.getByLabelText('Image Provider') as HTMLSelectElement;
      expect(select.value).toBe('wikimedia');

      // Change provider
      select.value = 'wikipedia';
      select.dispatchEvent(new Event('change', { bubbles: true }));

      await waitFor(() => {
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const thumbnails = (store.formData as any)?.realtime?.dashboard?.thumbnails;
        expect(thumbnails?.imageProvider).toBe('wikipedia');
      });
    });

    it('shows fallback policy when multiple providers available', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const fallbackSelect = screen.getByLabelText('Fallback Policy');
        expect(fallbackSelect).toBeInTheDocument();
      });

      const fallbackSelect = screen.getByLabelText('Fallback Policy') as HTMLSelectElement;
      expect(fallbackSelect.value).toBe('all');

      // Check options
      const options = within(fallbackSelect).getAllByRole('option');
      expect(options).toHaveLength(2);
      expect(options[0]).toHaveTextContent('Try all providers');
      expect(options[1]).toHaveTextContent('No fallback');
    });

    it('hides fallback policy when only one provider', async () => {
      // Mock single provider response
      mockApi.get.mockResolvedValue({
        providers: [{ value: 'wikipedia', display: 'Wikipedia' }],
      });

      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const imageProvider = screen.getByLabelText('Image Provider');
        expect(imageProvider).toBeInTheDocument();
      });

      // Fallback policy should not be visible
      expect(screen.queryByLabelText('Fallback Policy')).not.toBeInTheDocument();
    });
  });

  describe('Change Tracking', () => {
    it('tracks interface tab changes', async () => {
      // Mock to return true for interface settings
      mockHasSettingsChanged.mockImplementation((_original, current) => {
        // Check if this is the interface settings comparison
        if (current?.locale !== undefined || current?.newUI !== undefined) {
          return true;
        }
        return false;
      });

      testFactory.render();

      await waitFor(() => {
        // Interface tab should show change indicator badge
        const interfaceTab = screen.getByRole('tab', { name: /Interface Settings/i });
        expect(interfaceTab).toBeInTheDocument();
        const changeBadge = within(interfaceTab).queryByRole('status');
        expect(changeBadge).toBeInTheDocument();
      });
    });

    it('tracks dashboard tab changes', async () => {
      // Mock to return true for dashboard display settings
      mockHasSettingsChanged.mockImplementation((_original, current) => {
        // Check if this is the dashboard display comparison
        if (
          current?.summaryLimit !== undefined ||
          current?.summary !== undefined ||
          current?.mode !== undefined
        ) {
          return true;
        }
        return false;
      });

      testFactory.render();

      await waitFor(() => {
        // Dashboard tab should show change indicator badge
        const dashboardTab = screen.getByRole('tab', { name: /Dashboard Display/i });
        expect(dashboardTab).toBeInTheDocument();
        const changeBadge = within(dashboardTab).queryByRole('status');
        expect(changeBadge).toBeInTheDocument();
      });
    });

    it('shows no changes when settings match original', async () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render();

      await waitFor(() => {
        // No change badges should be present on tabs
        const interfaceTab = screen.getByRole('tab', { name: /Interface Settings/i });
        const dashboardTab = screen.getByRole('tab', { name: /Dashboard Display/i });
        expect(within(interfaceTab).queryByRole('status')).not.toBeInTheDocument();
        expect(within(dashboardTab).queryByRole('status')).not.toBeInTheDocument();
      });
    });
  });

  describe('API Integration', () => {
    it('loads image providers on mount', async () => {
      testFactory.render();

      await waitFor(() => {
        expect(mockApi.get).toHaveBeenCalledWith('/api/v2/settings/imageproviders');
      });

      // Switch to Dashboard tab to see providers
      await switchToTab('Dashboard Display');

      // Check that providers are populated
      const imageProviderSelect = screen.getByLabelText('Image Provider') as HTMLSelectElement;
      const options = within(imageProviderSelect).getAllByRole('option');
      expect(options).toHaveLength(2);
      expect(options[0]).toHaveTextContent('Wikimedia');
      expect(options[1]).toHaveTextContent('Wikipedia');
    });

    it('handles API error gracefully', async () => {
      const { ApiError } = await import('$lib/utils/api');
      const mockResponse = new Response('', { status: 500 });
      mockApi.get.mockRejectedValue(new ApiError('Network error', 500, mockResponse));

      testFactory.render();

      await waitFor(() => {
        expect(mockToastActions.warning).toHaveBeenCalledWith('Failed to load image providers');
      });

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      // Should have fallback provider
      const imageProviderSelect = screen.getByLabelText('Image Provider') as HTMLSelectElement;
      const options = within(imageProviderSelect).getAllByRole('option');
      expect(options).toHaveLength(1);
      expect(options[0]).toHaveTextContent('Wikipedia');
    });

    it('disables provider selection when loading', async () => {
      // Keep the promise pending to simulate loading state
      mockApi.get.mockImplementation(() => new Promise(() => {}));

      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      const imageProviderSelect = screen.getByLabelText('Image Provider') as HTMLSelectElement;
      expect(imageProviderSelect).toBeDisabled();
    });
  });

  describe('Disabled States', () => {
    it('disables interface controls when store is loading', async () => {
      // Set loading state
      settingsStore.update(state => ({ ...state, isLoading: true }));

      testFactory.render();

      await waitFor(() => {
        // Check interface form controls are disabled
        expect(screen.getByLabelText('Language')).toBeDisabled();
        expect(screen.getByLabelText('Use New User Interface')).toBeDisabled();
      });
    });

    it('disables dashboard controls when store is loading', async () => {
      // Set loading state
      settingsStore.update(state => ({ ...state, isLoading: true }));

      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        // Check dashboard form controls are disabled
        expect(screen.getByLabelText('Summary Limit')).toBeDisabled();
        expect(screen.getByLabelText('Show in Summary')).toBeDisabled();
        expect(screen.getByLabelText('Show in Recent')).toBeDisabled();
        expect(screen.getByLabelText('Image Provider')).toBeDisabled();
      });
    });

    it('disables all controls when store is saving', async () => {
      // Set saving state
      settingsStore.update(state => ({ ...state, isSaving: true }));

      testFactory.render();

      await waitFor(() => {
        // Check interface form controls are disabled
        expect(screen.getByLabelText('Language')).toBeDisabled();
        expect(screen.getByLabelText('Use New User Interface')).toBeDisabled();
      });

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        // Check dashboard form controls are disabled
        expect(screen.getByLabelText('Summary Limit')).toBeDisabled();
        expect(screen.getByLabelText('Show in Summary')).toBeDisabled();
        expect(screen.getByLabelText('Show in Recent')).toBeDisabled();
        expect(screen.getByLabelText('Image Provider')).toBeDisabled();
      });
    });

    it('disables image provider when only one provider available', async () => {
      // Mock single provider
      mockApi.get.mockResolvedValue({
        providers: [{ value: 'wikipedia', display: 'Wikipedia' }],
      });

      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const imageProviderSelect = screen.getByLabelText('Image Provider') as HTMLSelectElement;
        expect(imageProviderSelect).toBeDisabled();
      });
    });
  });

  describe('Default Values', () => {
    it('uses default values for interface tab when settings are not loaded', async () => {
      // Clear dashboard settings
      settingsStore.update(state => ({
        ...state,
        formData: {
          ...state.formData,

          realtime: {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            ...(state.formData as any)?.realtime,
            dashboard: undefined,
          },
        },
      }));

      testFactory.render();

      await waitFor(() => {
        // Check default value on Interface tab - checkbox uses standard label
        const newUICheckbox = screen.getByLabelText('Use New User Interface') as HTMLInputElement;
        expect(newUICheckbox.checked).toBe(false);

        // Language selector is rendered (uses custom SelectDropdown)
        expect(screen.getByText('Language')).toBeInTheDocument();
      });
    });

    it('uses default values for dashboard tab when settings are not loaded', async () => {
      // Clear dashboard settings
      settingsStore.update(state => ({
        ...state,
        formData: {
          ...state.formData,

          realtime: {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            ...(state.formData as any)?.realtime,
            dashboard: undefined,
          },
        },
      }));

      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        // Check default values on Dashboard tab
        const summaryLimit = screen.getByLabelText('Summary Limit') as HTMLInputElement;
        expect(summaryLimit.value).toBe('100');

        const summaryCheckbox = screen.getByLabelText('Show in Summary') as HTMLInputElement;
        expect(summaryCheckbox.checked).toBe(true);

        const recentCheckbox = screen.getByLabelText('Show in Recent') as HTMLInputElement;
        expect(recentCheckbox.checked).toBe(true);
      });
    });

    it('preserves existing locale in store when available', async () => {
      // Set specific locale
      settingsStore.update(state => ({
        ...state,
        formData: {
          ...state.formData,

          realtime: {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            ...(state.formData as any)?.realtime,
            dashboard: {
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              ...(state.formData as any)?.realtime?.dashboard,
              locale: 'de',
            },
          },
        },
      }));

      testFactory.render();

      await waitFor(() => {
        // Verify the store has the locale value (SelectDropdown uses custom UI)
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        expect((store.formData as any)?.realtime?.dashboard?.locale).toBe('de');

        // Language selector is rendered
        expect(screen.getByText('Language')).toBeInTheDocument();
      });
    });
  });

  describe('Update Handlers', () => {
    it('correctly updates nested thumbnail settings', async () => {
      testFactory.render();

      // Switch to Dashboard tab
      await switchToTab('Dashboard Display');

      await waitFor(() => {
        const fallbackSelect = screen.getByLabelText('Fallback Policy') as HTMLSelectElement;
        expect(fallbackSelect).toBeInTheDocument();
      });

      const fallbackSelect = screen.getByLabelText('Fallback Policy') as HTMLSelectElement;

      // Change fallback policy
      fallbackSelect.value = 'none';
      fallbackSelect.dispatchEvent(new Event('change', { bubbles: true }));

      await waitFor(() => {
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const dashboard = (store.formData as any)?.realtime?.dashboard;

        // Check that other thumbnail settings are preserved
        expect(dashboard?.thumbnails?.summary).toBe(true);
        expect(dashboard?.thumbnails?.recent).toBe(true);
        expect(dashboard?.thumbnails?.imageProvider).toBe('wikimedia');
        expect(dashboard?.thumbnails?.fallbackPolicy).toBe('none');
      });
    });

    it('renders language selector alongside other dashboard settings', async () => {
      testFactory.render();

      await waitFor(() => {
        // Verify dashboard settings in store are intact
        const store = get(settingsStore);
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const dashboard = (store.formData as any)?.realtime?.dashboard;

        // Check that settings are preserved
        expect(dashboard?.summaryLimit).toBe(100);
        expect(dashboard?.thumbnails?.summary).toBe(true);
        expect(dashboard?.thumbnails?.recent).toBe(true);
        expect(dashboard?.newUI).toBe(false);
        expect(dashboard?.locale).toBe('en');

        // Language selector is rendered (SelectDropdown uses custom button-based UI)
        expect(screen.getByText('Language')).toBeInTheDocument();
      });
    });
  });
});

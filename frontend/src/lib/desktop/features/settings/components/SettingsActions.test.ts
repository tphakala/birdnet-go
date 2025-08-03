import { describe, it, expect, vi, beforeEach } from 'vitest';
import { writable } from 'svelte/store';
import {
  screen,
  fireEvent,
  createI18nMock,
  renderTyped,
  mockDOMAPIs,
  waitFor,
} from '../../../../../test/render-helpers';
import SettingsActions from './SettingsActions.svelte';

// Mock logger type definition
interface MockLogger {
  error: ReturnType<typeof vi.fn>;
  warn: ReturnType<typeof vi.fn>;
  info: ReturnType<typeof vi.fn>;
  debug: ReturnType<typeof vi.fn>;
}

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'settings.actions.reset': 'Reset',
    'settings.actions.resetAriaLabel': 'Reset all settings to original values',
    'settings.actions.save': 'Save Settings',
    'settings.actions.saving': 'Saving...',
    'settings.actions.saveAriaLabel': 'Save settings',
    'settings.actions.savingAriaLabel': 'Saving settings',
  }),
}));

// Mock logger
vi.mock('$lib/utils/logger', () => ({
  loggers: {
    settings: {
      error: vi.fn(),
      warn: vi.fn(),
      info: vi.fn(),
      debug: vi.fn(),
    },
  },
}));

// Mock LoadingSpinner component to avoid import issues
vi.mock('$lib/desktop/components/ui/LoadingSpinner.svelte');

// Set up DOM APIs
mockDOMAPIs();

// Create mock stores and actions
const createMockStores = () => {
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    formData: { test: 'value' },
    originalData: { test: 'original' },
  });

  const hasUnsavedChanges = writable(false);

  const settingsActions = {
    saveSettings: vi.fn().mockResolvedValue(undefined),
    resetAllSettings: vi.fn(),
  };

  return {
    settingsStore,
    hasUnsavedChanges,
    settingsActions,
  };
};

// Mock the settings stores module
let mockStores = createMockStores();

vi.mock('$lib/stores/settings.js', () => ({
  get settingsStore() {
    return mockStores.settingsStore;
  },
  get hasUnsavedChanges() {
    return mockStores.hasUnsavedChanges;
  },
  get settingsActions() {
    return mockStores.settingsActions;
  },
}));

describe('SettingsActions', () => {
  let mockSettingsLogger: MockLogger;

  beforeEach(async () => {
    vi.clearAllMocks();
    mockStores = createMockStores();
    // Get the mocked logger
    const { loggers } =
      await vi.importMock<typeof import('$lib/utils/logger')>('$lib/utils/logger');
    mockSettingsLogger = loggers.settings as MockLogger;
    mockSettingsLogger.error.mockClear();
  });

  describe('Rendering', () => {
    it('renders save button by default', () => {
      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      expect(saveButton).toBeInTheDocument();
      expect(saveButton).toHaveTextContent('Save Settings');
      expect(saveButton).toBeDisabled(); // No unsaved changes
    });

    it('does not show reset button when no unsaved changes', () => {
      renderTyped(SettingsActions);

      expect(
        screen.queryByRole('button', { name: 'Reset all settings to original values' })
      ).not.toBeInTheDocument();
    });

    it('shows reset button when there are unsaved changes', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      expect(resetButton).toBeInTheDocument();
      expect(resetButton).toHaveTextContent('Reset');
    });

    it('has correct layout classes', () => {
      renderTyped(SettingsActions);

      const container = screen.getByRole('button', { name: 'Save settings' }).parentElement;
      expect(container).toHaveClass('flex', 'justify-end', 'items-center', 'gap-3', 'mt-6', 'pt-6');
    });
  });

  describe('Save Button States', () => {
    it('is disabled when no unsaved changes', () => {
      mockStores.hasUnsavedChanges.set(false);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      expect(saveButton).toBeDisabled();
    });

    it('is enabled when there are unsaved changes', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      expect(saveButton).not.toBeDisabled();
    });

    it('is disabled while saving', () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Saving settings' });
      expect(saveButton).toBeDisabled();
    });

    it('shows loading spinner and text while saving', () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Saving settings' });
      expect(saveButton).toHaveTextContent('Saving...');
      // LoadingSpinner is rendered inside the button when isSaving is true
      expect(saveButton).toHaveAttribute('aria-busy', 'true');
    });
  });

  describe('Reset Button', () => {
    it('is disabled while saving', () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      expect(resetButton).toBeDisabled();
    });

    it('has correct styling', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      expect(resetButton).toHaveClass('btn', 'btn-ghost', 'btn-sm');
    });
  });

  describe('Save Functionality', () => {
    it('calls saveSettings when save button clicked', async () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      fireEvent.click(saveButton);

      await waitFor(() => {
        expect(mockStores.settingsActions.saveSettings).toHaveBeenCalledTimes(1);
      });
    });

    it('handles save errors gracefully', async () => {
      const error = new Error('Save failed');
      mockStores.settingsActions.saveSettings.mockRejectedValueOnce(error);
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      fireEvent.click(saveButton);

      await waitFor(() => {
        expect(mockSettingsLogger.error).toHaveBeenCalledWith('Failed to save settings:', error);
      });
    });

    it('does not call save when button is disabled', () => {
      mockStores.hasUnsavedChanges.set(false);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      expect(saveButton).toBeDisabled();

      // In Svelte, disabled buttons still fire click events in tests
      // The component should check the disabled state internally
      fireEvent.click(saveButton);

      // Since the button is disabled (no unsaved changes), save should not be called
      expect(mockStores.settingsActions.saveSettings).not.toHaveBeenCalled();
    });
  });

  describe('Reset Functionality', () => {
    it('calls resetAllSettings when reset button clicked', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      fireEvent.click(resetButton);

      expect(mockStores.settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
    });

    it('does not call reset when button is disabled', () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      expect(resetButton).toBeDisabled();

      // In Svelte, disabled buttons still fire click events in tests
      // The component should check the disabled state internally
      fireEvent.click(resetButton);

      // Since the button is disabled (isSaving), reset should not be called
      expect(mockStores.settingsActions.resetAllSettings).not.toHaveBeenCalled();
    });
  });

  describe('Reactive Updates', () => {
    it('updates when unsaved changes state changes', async () => {
      renderTyped(SettingsActions);

      // Initially no reset button
      expect(
        screen.queryByRole('button', { name: 'Reset all settings to original values' })
      ).not.toBeInTheDocument();

      // Update store
      mockStores.hasUnsavedChanges.set(true);

      // Force component to re-render
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: 'Reset all settings to original values' })
        ).toBeInTheDocument();
      });
    });

    it('updates save button state when saving starts', async () => {
      mockStores.hasUnsavedChanges.set(true);
      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      expect(saveButton).toHaveTextContent('Save Settings');

      // Start saving
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Saving settings' })).toHaveTextContent(
          'Saving...'
        );
      });
    });

    it('updates save button state when saving completes', async () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      expect(screen.getByRole('button', { name: 'Saving settings' })).toHaveTextContent(
        'Saving...'
      );

      // Complete saving
      mockStores.settingsStore.update(store => ({ ...store, isSaving: false }));
      mockStores.hasUnsavedChanges.set(false);

      await waitFor(() => {
        const saveButton = screen.getByRole('button', { name: 'Save settings' });
        expect(saveButton).toHaveTextContent('Save Settings');
        expect(saveButton).toBeDisabled();
      });
    });
  });

  describe('Icons', () => {
    it('renders refresh icon in reset button', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      // Check for SVG content (refresh icon)
      expect(resetButton.innerHTML).toContain('svg');
    });
  });

  describe('Accessibility', () => {
    it('has proper ARIA labels', () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      expect(screen.getByRole('button', { name: 'Save settings' })).toHaveAttribute(
        'aria-label',
        'Save settings'
      );

      expect(
        screen.getByRole('button', { name: 'Reset all settings to original values' })
      ).toHaveAttribute('aria-label', 'Reset all settings to original values');
    });

    it('updates ARIA label when saving', () => {
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Saving settings' });
      expect(saveButton).toHaveAttribute('aria-label', 'Saving settings');
      expect(saveButton).toHaveAttribute('aria-busy', 'true');
    });
  });

  describe('Edge Cases', () => {
    it('handles rapid save clicks', async () => {
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });

      // Click multiple times rapidly
      fireEvent.click(saveButton);
      fireEvent.click(saveButton);
      fireEvent.click(saveButton);

      // In real scenarios, the button would be disabled after first click
      // But in tests, we're verifying the handler is called for each click
      await waitFor(() => {
        expect(mockStores.settingsActions.saveSettings).toHaveBeenCalled();
      });
    });

    it('handles save promise rejection without crashing', async () => {
      mockStores.settingsActions.saveSettings.mockRejectedValueOnce(new Error('Network error'));
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });

      // Should not throw
      fireEvent.click(saveButton);

      await waitFor(() => {
        expect(mockSettingsLogger.error).toHaveBeenCalled();
      });
    });

    it('handles store updates during save', async () => {
      mockStores.hasUnsavedChanges.set(true);

      // Mock saveSettings to update store during save
      mockStores.settingsActions.saveSettings.mockImplementation(async () => {
        mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));
        await new Promise(resolve => setTimeout(resolve, 100));
        mockStores.settingsStore.update(store => ({ ...store, isSaving: false }));
        mockStores.hasUnsavedChanges.set(false);
      });

      renderTyped(SettingsActions);

      const saveButton = screen.getByRole('button', { name: 'Save settings' });
      fireEvent.click(saveButton);

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled();
      });
    });
  });

  describe('Integration', () => {
    it('works with complete save flow', async () => {
      // Start with changes
      mockStores.hasUnsavedChanges.set(true);

      renderTyped(SettingsActions);

      // Should show both buttons
      expect(screen.getByRole('button', { name: 'Save settings' })).not.toBeDisabled();
      expect(
        screen.getByRole('button', { name: 'Reset all settings to original values' })
      ).toBeInTheDocument();

      // Click save
      fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));

      // Simulate successful save
      mockStores.settingsStore.update(store => ({ ...store, isSaving: true }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Saving settings' })).toBeInTheDocument();
      });

      // Complete save
      mockStores.settingsStore.update(store => ({ ...store, isSaving: false }));
      mockStores.hasUnsavedChanges.set(false);

      await waitFor(() => {
        // Save button should be disabled
        expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled();
        // Reset button should be gone
        expect(
          screen.queryByRole('button', { name: 'Reset all settings to original values' })
        ).not.toBeInTheDocument();
      });
    });

    it('works with reset flow', () => {
      // Start with changes
      mockStores.hasUnsavedChanges.set(true);
      mockStores.settingsStore.update(store => ({
        ...store,
        formData: { test: 'modified' },
        originalData: { test: 'original' },
      }));

      renderTyped(SettingsActions);

      // Click reset
      const resetButton = screen.getByRole('button', {
        name: 'Reset all settings to original values',
      });
      fireEvent.click(resetButton);

      expect(mockStores.settingsActions.resetAllSettings).toHaveBeenCalled();
    });
  });
});

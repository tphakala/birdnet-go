import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  screen,
  createI18nMock,
  createComponentTestFactory,
  mockDOMAPIs,
  waitFor,
} from '../../../../../test/render-helpers';
import SettingsSection from './SettingsSection.svelte';
import SettingsSectionWrapper from './test-helpers/SettingsSectionWrapper.test.svelte';

// Mock i18n translations using the shared helper
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'settings.card.changedAriaLabel': 'Settings changed',
    'settings.card.changed': 'Changed',
  }),
}));

// Mock the hasSettingsChanged function
vi.mock('$lib/utils/settingsChanges', () => ({
  hasSettingsChanged: vi.fn(),
}));

// Set up DOM APIs
mockDOMAPIs();

// Create test factory
const testFactory = createComponentTestFactory(SettingsSection);
const wrapperFactory = createComponentTestFactory(SettingsSectionWrapper);

describe('SettingsSection', () => {
  let mockHasSettingsChanged: ReturnType<typeof vi.fn>;

  beforeEach(async () => {
    vi.clearAllMocks();
    // Get the mocked function
    const mocked = await vi.importMock<typeof import('$lib/utils/settingsChanges')>(
      '$lib/utils/settingsChanges'
    );
    mockHasSettingsChanged = mocked.hasSettingsChanged as ReturnType<typeof vi.fn>;
    // Default to no changes
    mockHasSettingsChanged.mockReturnValue(false);
  });

  describe('Rendering', () => {
    it('renders with basic props', () => {
      testFactory.render({
        title: 'Test Section',
        description: 'Test description',
      });

      expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Test Section');
      expect(screen.getByText('Test description')).toBeInTheDocument();
    });

    it('renders children content', () => {
      wrapperFactory.render({
        title: 'Section with Content',
        childContent: 'Custom content',
      });

      expect(screen.getByTestId('section-content')).toHaveTextContent('Custom content');
    });

    it('renders with all props', () => {
      // When hasChanges is explicitly true, it should be passed through
      testFactory.render({
        title: 'Complete Section',
        description: 'Full description',
        className: 'custom-section',
        hasChanges: true,
        defaultOpen: true,
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toHaveClass('custom-section');
      // The change indicator should be shown
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });
  });

  describe('Change Detection', () => {
    it('uses explicit hasChanges prop when provided', () => {
      testFactory.render({
        title: 'Test Section',
        hasChanges: true,
      });

      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
      // Should not call hasSettingsChanged when explicit prop is provided
      expect(mockHasSettingsChanged).not.toHaveBeenCalled();
    });

    it('shows hasChanges false when explicitly set to false', () => {
      testFactory.render({
        title: 'Test Section',
        hasChanges: false,
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
      expect(mockHasSettingsChanged).not.toHaveBeenCalled();
    });

    it('detects changes using originalData and currentData when hasChanges not provided', () => {
      mockHasSettingsChanged.mockReturnValue(true);

      const originalData = { test: 'original' };
      const currentData = { test: 'modified' };

      testFactory.render({
        title: 'Test Section',
        originalData,
        currentData,
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith(originalData, currentData);
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });

    it('returns false when originalData and currentData are identical', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      const data = { test: 'same' };

      testFactory.render({
        title: 'Test Section',
        originalData: data,
        currentData: data,
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith(data, data);
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });

    it('returns false when originalData or currentData is missing', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Test Section',
        originalData: { test: 'data' },
        // currentData is missing
      });

      // hasSettingsChanged is called even when data is missing
      expect(mockHasSettingsChanged).toHaveBeenCalledWith({ test: 'data' }, undefined);
      // But returns false for missing data
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });

    it('returns false when only currentData is provided', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Test Section',
        currentData: { test: 'data' },
        // originalData is missing
      });

      // hasSettingsChanged is called even when data is missing
      expect(mockHasSettingsChanged).toHaveBeenCalledWith(undefined, { test: 'data' });
      // But returns false for missing data
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });

    it('prioritizes explicit hasChanges over automatic detection', () => {
      mockHasSettingsChanged.mockReturnValue(true);

      const originalData = { test: 'original' };
      const currentData = { test: 'modified' };

      testFactory.render({
        title: 'Test Section',
        hasChanges: false, // Explicit false
        originalData,
        currentData,
      });

      // Should not call hasSettingsChanged when explicit prop is provided
      expect(mockHasSettingsChanged).not.toHaveBeenCalled();
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });
  });

  describe('Deep Equality Performance', () => {
    it('handles complex nested data changes efficiently', () => {
      mockHasSettingsChanged.mockReturnValue(true);

      const originalData = {
        audio: {
          source: 'default',
          export: { enabled: false, format: 'wav', quality: 'high' },
          filters: [
            { type: 'lowpass', frequency: 1000 },
            { type: 'highpass', frequency: 100 },
          ],
        },
        settings: {
          nested: {
            deeply: {
              value: 'original',
            },
          },
        },
      };

      const currentData = {
        audio: {
          source: 'default',
          export: { enabled: true, format: 'wav', quality: 'high' }, // Changed
          filters: [
            { type: 'lowpass', frequency: 1000 },
            { type: 'highpass', frequency: 100 },
          ],
        },
        settings: {
          nested: {
            deeply: {
              value: 'original',
            },
          },
        },
      };

      testFactory.render({
        title: 'Audio Settings',
        originalData,
        currentData,
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith(originalData, currentData);
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });

    it('handles arrays with different lengths', () => {
      mockHasSettingsChanged.mockReturnValue(true);

      const originalData = {
        items: ['a', 'b', 'c'],
      };

      const currentData = {
        items: ['a', 'b', 'c', 'd'],
      };

      testFactory.render({
        title: 'Array Test',
        originalData,
        currentData,
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith(originalData, currentData);
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });

    it('handles circular references gracefully', () => {
      // Create objects with circular references
      interface CircularObject {
        name: string;
        self?: CircularObject;
      }

      const originalData: CircularObject = { name: 'original' };
      originalData.self = originalData;

      const currentData: CircularObject = { name: 'original' };
      currentData.self = currentData;

      // hasSettingsChanged should handle circular refs
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Circular Ref Test',
        originalData,
        currentData,
      });

      // The mock was called, but we need to check the actual values passed
      expect(mockHasSettingsChanged).toHaveBeenCalled();
      const [firstArg, secondArg] = mockHasSettingsChanged.mock.calls[0];

      // Both should be circular objects with name 'original' and self reference
      expect(firstArg).toHaveProperty('name', 'original');
      expect(firstArg).toHaveProperty('self');
      expect(secondArg).toHaveProperty('name', 'original');
      expect(secondArg).toHaveProperty('self');

      // hasSettingsChanged returns false for identical circular structures
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });
  });

  describe('Reactive Updates', () => {
    it('updates when data changes', async () => {
      mockHasSettingsChanged.mockReturnValue(false);

      const { rerender } = testFactory.render({
        title: 'Reactive Test',
        originalData: { value: 1 },
        currentData: { value: 1 },
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();

      // Change data
      mockHasSettingsChanged.mockReturnValue(true);

      await rerender({
        title: 'Reactive Test',
        originalData: { value: 1 },
        currentData: { value: 2 },
      });

      await waitFor(() => {
        expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
      });
    });

    it('updates when hasChanges prop changes', async () => {
      const { rerender } = testFactory.render({
        title: 'Prop Update Test',
        hasChanges: false,
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();

      await rerender({
        title: 'Prop Update Test',
        hasChanges: true,
      });

      await waitFor(() => {
        expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
      });
    });
  });

  describe('Props Forwarding', () => {
    it('passes through all props to SettingsCard', () => {
      testFactory.render({
        title: 'Test Section',
        description: 'Test description',
        defaultOpen: false,
        className: 'custom-class',
        padding: false,
        'data-custom': 'value',
        'aria-label': 'Custom section',
      });

      const container = screen.getByTestId('settings-card');
      expect(container).toHaveClass('custom-class');
      expect(container).toHaveAttribute('data-custom', 'value');
      expect(container).toHaveAttribute('aria-label', 'Custom section');
    });

    it('forwards snippet props correctly', () => {
      wrapperFactory.render({
        title: 'Snippet Test',
        showCustomHeader: true,
        headerContent: 'Custom Header Content',
        showCustomFooter: true,
        footerContent: 'Custom Footer Content',
      });

      expect(screen.getByTestId('custom-header')).toHaveTextContent('Custom Header Content');
      expect(screen.getByTestId('custom-footer')).toHaveTextContent('Custom Footer Content');
    });
  });

  describe('Edge Cases', () => {
    it('handles undefined props gracefully', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Edge Case Test',
        originalData: undefined,
        currentData: undefined,
        hasChanges: undefined,
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
      // hasSettingsChanged is called when hasChanges is not explicitly provided
      expect(mockHasSettingsChanged).toHaveBeenCalledWith(undefined, undefined);
    });

    it('handles null data values', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Null Test',
        originalData: null,
        currentData: { value: 'something' },
      });

      // hasSettingsChanged is called even with null values
      expect(mockHasSettingsChanged).toHaveBeenCalledWith(null, { value: 'something' });
      // hasSettingsChanged returns false for null values
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });

    it('handles empty objects', () => {
      mockHasSettingsChanged.mockReturnValue(false);

      testFactory.render({
        title: 'Empty Object Test',
        originalData: {},
        currentData: {},
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith({}, {});
      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
    });
  });

  describe('Accessibility', () => {
    it('provides proper ARIA attributes for change indicator', () => {
      testFactory.render({
        title: 'Accessible Section',
        hasChanges: true,
      });

      const badge = screen.getByRole('status', { name: 'Settings changed' });
      expect(badge).toHaveAttribute('aria-label', 'Settings changed');
    });

    it('maintains heading hierarchy', () => {
      testFactory.render({
        title: 'Heading Test',
        description: 'Testing heading levels',
      });

      const heading = screen.getByRole('heading', { level: 3 });
      expect(heading).toHaveTextContent('Heading Test');
    });

    it('preserves semantic structure with children', () => {
      wrapperFactory.render({
        title: 'Semantic Test',
        childContent: 'Test content for semantic structure',
      });

      // The wrapper renders content as text, not HTML
      const content = screen.getByTestId('section-content');
      expect(content).toBeInTheDocument();
      expect(content).toHaveTextContent('Test content for semantic structure');
    });
  });

  describe('Integration with SettingsCard', () => {
    it('inherits SettingsCard behavior', () => {
      testFactory.render({
        title: 'Integration Test',
        description: 'Testing card integration',
        className: 'section-test',
        hasChanges: true,
      });

      const card = screen.getByTestId('settings-card');
      expect(card).toBeInTheDocument();
      expect(card).toHaveClass('card', 'bg-base-100', 'shadow-xs', 'section-test');
    });

    it('properly computes derived change state', async () => {
      // Test the $derived reactivity
      mockHasSettingsChanged.mockReturnValue(false);

      const { rerender } = testFactory.render({
        title: 'Derived Test',
        originalData: { value: 1 },
        currentData: { value: 1 },
      });

      expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
      expect(mockHasSettingsChanged).toHaveBeenCalledWith({ value: 1 }, { value: 1 });

      // Clear previous calls and set new return value
      mockHasSettingsChanged.mockClear();
      mockHasSettingsChanged.mockReturnValue(true);

      // Simulate data change
      await rerender({
        title: 'Derived Test',
        originalData: { value: 1 },
        currentData: { value: 2 },
      });

      // The derived state should update
      expect(mockHasSettingsChanged).toHaveBeenCalledWith({ value: 1 }, { value: 2 });
      await waitFor(() => {
        expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
      });
    });
  });

  describe('Performance Considerations', () => {
    it('memoizes change detection result', () => {
      mockHasSettingsChanged.mockReturnValue(true);

      const originalData = { complex: { nested: { data: 'value' } } };
      const currentData = { complex: { nested: { data: 'changed' } } };

      testFactory.render({
        title: 'Performance Test',
        originalData,
        currentData,
      });

      // Initial render
      expect(mockHasSettingsChanged).toHaveBeenCalledTimes(1);

      // The component uses $derived which should memoize the result
      // and not recalculate unless dependencies change
    });

    it('handles large datasets efficiently', () => {
      const largeOriginal = {
        items: Array.from({ length: 1000 }, (_, i) => ({
          id: i,
          name: `Item ${i}`,
          value: Math.random(),
        })),
      };

      const largeCurrent = {
        items: [...largeOriginal.items],
      };
      largeCurrent.items[500] = { ...largeCurrent.items[500], value: 999 };

      mockHasSettingsChanged.mockReturnValue(true);

      testFactory.render({
        title: 'Large Dataset Test',
        originalData: largeOriginal,
        currentData: largeCurrent,
      });

      expect(mockHasSettingsChanged).toHaveBeenCalledWith(largeOriginal, largeCurrent);
      expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
    });
  });
});

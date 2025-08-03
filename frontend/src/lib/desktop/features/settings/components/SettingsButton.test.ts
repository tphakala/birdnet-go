import { describe, it, expect, vi } from 'vitest';
import {
  screen,
  fireEvent,
  createI18nMock,
  createComponentTestFactory,
  mockDOMAPIs,
  waitFor,
} from '../../../../../test/render-helpers';
import SettingsButton from './SettingsButton.svelte';

// Import wrapper for testing snippets
import SettingsButtonWrapper from './test-helpers/SettingsButtonWrapper.test.svelte';

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'common.loading': 'Loading...',
    'common.testLoading': 'Testing...',
  }),
}));

// Set up DOM APIs for tests
mockDOMAPIs();

// Create test factory
const testFactory = createComponentTestFactory(SettingsButton);
const wrapperFactory = createComponentTestFactory(SettingsButtonWrapper);

describe('SettingsButton', () => {
  describe('Rendering', () => {
    it('renders with default props', () => {
      wrapperFactory.render({
        childContent: 'Click Me',
      });

      const button = screen.getByRole('button');
      expect(button).toBeInTheDocument();
      expect(button).toHaveTextContent('Click Me');
      expect(button).toHaveClass('settings-button');
      expect(button).not.toBeDisabled();
    });

    it('applies custom className', () => {
      wrapperFactory.render({
        className: 'custom-button test-class',
        childContent: 'Button',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('settings-button', 'custom-button', 'test-class');
    });

    it('renders loading state with default text', () => {
      testFactory.render({
        loading: true,
      });

      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Loading...');
      expect(button.querySelector('.loading-spinner')).toBeInTheDocument();
      expect(button).toHaveAttribute('aria-busy', 'true');
    });

    it('renders loading state with custom loading text', () => {
      testFactory.render({
        loading: true,
        loadingText: 'Testing...',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Testing...');
    });

    it('does not render children when loading', () => {
      wrapperFactory.render({
        loading: true,
        childContent: 'Should not appear',
      });

      expect(screen.queryByTestId('button-content')).not.toBeInTheDocument();
      expect(screen.getByRole('button')).toHaveTextContent('Loading...');
    });
  });

  describe('Disabled State', () => {
    it('is disabled when disabled prop is true', () => {
      wrapperFactory.render({
        disabled: true,
        childContent: 'Disabled Button',
      });

      const button = screen.getByRole('button');
      expect(button).toBeDisabled();
      expect(button).toHaveClass('settings-button--disabled');
    });

    it('is disabled when loading', () => {
      testFactory.render({
        loading: true,
      });

      const button = screen.getByRole('button');
      expect(button).toBeDisabled();
      expect(button).toHaveClass('settings-button--disabled');
    });

    it('is disabled when both loading and disabled are true', () => {
      testFactory.render({
        loading: true,
        disabled: true,
      });

      const button = screen.getByRole('button');
      expect(button).toBeDisabled();
      expect(button).toHaveClass('settings-button--disabled');
    });

    it('prevents click events when disabled', () => {
      const handleClick = vi.fn();

      wrapperFactory.render({
        onclick: handleClick,
        disabled: true,
        childContent: 'Disabled',
      });

      const button = screen.getByRole('button');
      fireEvent.click(button);

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('prevents click events when loading', () => {
      const handleClick = vi.fn();

      testFactory.render({
        onclick: handleClick,
        loading: true,
      });

      const button = screen.getByRole('button');
      fireEvent.click(button);

      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe('Click Events', () => {
    it('calls onclick handler when clicked', () => {
      const handleClick = vi.fn();

      wrapperFactory.render({
        onclick: handleClick,
        childContent: 'Click Me',
      });

      const button = screen.getByRole('button');
      fireEvent.click(button);

      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('does not call onclick when no handler provided', () => {
      wrapperFactory.render({
        childContent: 'No Handler',
      });

      const button = screen.getByRole('button');

      // Should not throw
      expect(() => fireEvent.click(button)).not.toThrow();
    });

    it('supports multiple clicks', () => {
      const handleClick = vi.fn();

      wrapperFactory.render({
        onclick: handleClick,
        childContent: 'Multi Click',
      });

      const button = screen.getByRole('button');
      fireEvent.click(button);
      fireEvent.click(button);
      fireEvent.click(button);

      expect(handleClick).toHaveBeenCalledTimes(3);
    });
  });

  describe('CSS Classes and Styling', () => {
    it('applies base button styles', () => {
      wrapperFactory.render({
        childContent: 'Styled Button',
      });

      const button = screen.getByRole('button');

      // Check for required style classes
      expect(button).toHaveClass('settings-button');
      expect(button).not.toHaveClass('settings-button--disabled');
    });

    it('applies disabled styles when disabled', () => {
      wrapperFactory.render({
        disabled: true,
        childContent: 'Disabled',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveClass('settings-button--disabled');
    });

    it('has proper type attribute', () => {
      wrapperFactory.render({
        childContent: 'Button',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('type', 'button');
    });
  });

  describe('Loading Spinner', () => {
    it('shows loading spinner with correct classes', () => {
      testFactory.render({
        loading: true,
      });

      const spinner = screen.getByRole('button').querySelector('.loading');
      expect(spinner).toBeInTheDocument();
      expect(spinner).toHaveClass('loading', 'loading-spinner', 'loading-sm');
    });

    it('removes spinner when loading completes', async () => {
      const { rerender } = testFactory.render({
        loading: true,
      });

      expect(screen.getByRole('button').querySelector('.loading')).toBeInTheDocument();

      await rerender({ loading: false });

      expect(screen.getByRole('button').querySelector('.loading')).not.toBeInTheDocument();
      // Spinner should be removed
    });
  });

  describe('Accessibility', () => {
    it('has proper button role', () => {
      wrapperFactory.render({
        childContent: 'Accessible Button',
      });

      expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('has aria-busy when loading', () => {
      testFactory.render({
        loading: true,
      });

      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-busy', 'true');
    });

    it('does not have aria-busy when not loading', () => {
      wrapperFactory.render({
        childContent: 'Not Loading',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveAttribute('aria-busy', 'false');
    });

    it('maintains focus state', () => {
      wrapperFactory.render({
        childContent: 'Focusable',
      });

      const button = screen.getByRole('button');
      button.focus();

      expect(document.activeElement).toBe(button);
    });
  });

  describe('State Transitions', () => {
    it('transitions from normal to loading state', async () => {
      const { rerender } = wrapperFactory.render({
        childContent: 'Submit',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Submit');
      expect(button).not.toBeDisabled();

      await rerender({ loading: true });

      expect(button).toHaveTextContent('Loading...');
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('aria-busy', 'true');
    });

    it('transitions from loading to normal state', async () => {
      const { rerender } = testFactory.render({
        loading: true,
      });

      expect(screen.getByRole('button')).toHaveTextContent('Loading...');

      await rerender({
        loading: false,
      });

      const button = screen.getByRole('button');
      expect(button).not.toHaveTextContent('Loading...');
      expect(button).not.toBeDisabled();
      expect(button).toHaveAttribute('aria-busy', 'false');
    });

    it('updates loading text dynamically', async () => {
      const { rerender } = testFactory.render({
        loading: true,
        loadingText: 'Processing...',
      });

      expect(screen.getByRole('button')).toHaveTextContent('Processing...');

      await rerender({
        loading: true,
        loadingText: 'Almost done...',
      });

      expect(screen.getByRole('button')).toHaveTextContent('Almost done...');
    });
  });

  describe('Edge Cases', () => {
    it('handles empty children gracefully', () => {
      wrapperFactory.render({
        childContent: '',
      });

      const button = screen.getByRole('button');
      expect(button).toBeInTheDocument();
      expect(button).toHaveTextContent('');
    });

    it('handles rapid state changes', async () => {
      const { rerender } = testFactory.render({
        loading: true,
      });

      // Rapid state changes
      await rerender({ loading: false });
      await rerender({ loading: true });
      await rerender({ loading: false });
      await rerender({ loading: true });

      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Loading...');
      expect(button).toBeDisabled();
    });

    it('handles onclick being set to null', async () => {
      const { rerender } = wrapperFactory.render({
        onclick: vi.fn(),
        childContent: 'Click',
      });

      // Change onclick to undefined
      await rerender({
        onclick: undefined,
      });

      const button = screen.getByRole('button');

      // Should not throw when clicked
      expect(() => fireEvent.click(button)).not.toThrow();
    });
  });

  describe('Integration Patterns', () => {
    it('works with form submission pattern', async () => {
      const handleSubmit = vi.fn(async () => {
        // Simulate async operation
        await new Promise(resolve => setTimeout(resolve, 50));
      });

      // Render initially not loading
      const { rerender } = wrapperFactory.render({
        loading: false,
        onclick: async () => {
          await handleSubmit();
        },
        childContent: 'Submit',
      });

      const button = screen.getByRole('button');
      expect(button).toHaveTextContent('Submit');
      expect(button).not.toBeDisabled();

      // Test loading state separately
      await rerender({ loading: true });

      const loadingButton = screen.getByRole('button');
      expect(loadingButton).toHaveTextContent('Loading...');
      expect(loadingButton).toBeDisabled();

      // Test back to normal state
      await rerender({ loading: false });

      const normalButton = screen.getByRole('button');
      expect(normalButton).toHaveTextContent('Submit');
      expect(normalButton).not.toBeDisabled();

      // Test actual click
      fireEvent.click(normalButton);
      await waitFor(() => {
        expect(handleSubmit).toHaveBeenCalled();
      });
    });

    it('works as a test connection button', async () => {
      // Test different connection states
      const { rerender } = wrapperFactory.render({
        loading: false,
        loadingText: 'Testing Connection...',
        disabled: false,
        childContent: 'Test Connection',
      });

      let button = screen.getByRole('button');
      expect(button).toHaveTextContent('Test Connection');
      expect(button).not.toBeDisabled();

      // Simulate testing state - when loading, the button should be disabled
      await rerender({
        loading: true,
        loadingText: 'Testing Connection...',
        disabled: false,
        childContent: 'Test Connection',
      });

      // Check button state after rerender
      button = screen.getByRole('button');
      // The button shows loading text when loading is true
      expect(button).toHaveTextContent('Testing Connection...');
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('aria-busy', 'true');
    });
  });
});

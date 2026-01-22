import { describe, it, expect, vi } from 'vitest';
import { renderTyped, screen, fireEvent, createI18nMock } from '../../../../test/render-helpers';
import ErrorAlert from './ErrorAlert.svelte';
import ErrorAlertTestWrapper from './ErrorAlert.test.svelte';
import type { ComponentProps } from 'svelte';

// Mock i18n translations using the shared helper
vi.mock('$lib/i18n', () => ({
  t: createI18nMock({
    'common.aria.dismissAlert': 'Dismiss alert',
  }),
}));

// Helper function to render ErrorAlert with proper typing
const renderErrorAlert = (props: Partial<ComponentProps<typeof ErrorAlert>>) => {
  return renderTyped(ErrorAlert, { props });
};

describe('ErrorAlert', () => {
  it('renders with default props', () => {
    renderErrorAlert({ message: 'An error occurred' });

    const alert = screen.getByRole('alert');
    expect(alert).toBeInTheDocument();
    // Now uses native Tailwind classes with CSS variables
    expect(alert.className).toContain('bg-[color-mix(in_srgb,var(--color-error)');
    expect(screen.getByText('An error occurred')).toBeInTheDocument();
  });

  it('renders different alert types', () => {
    const types = ['error', 'warning', 'info', 'success'] as const;

    types.forEach(type => {
      const { unmount } = renderErrorAlert({
        message: `${type} message`,
        type,
      });

      const alert = screen.getByRole('alert');
      // Check for type-specific CSS variable class
      expect(alert.className).toContain(`bg-[color-mix(in_srgb,var(--color-${type})`);

      unmount();
    });
  });

  it('renders with custom children', () => {
    renderTyped(ErrorAlertTestWrapper, {
      props: {
        showChildren: true,
      },
    });

    expect(screen.getByText('Custom error content')).toBeInTheDocument();
    expect(screen.getByText('With a link')).toBeInTheDocument();
  });

  it('shows dismiss button when dismissible', () => {
    renderErrorAlert({
      message: 'Dismissible alert',
      dismissible: true,
    });

    const dismissButton = screen.getByLabelText('Dismiss alert');
    expect(dismissButton).toBeInTheDocument();
  });

  it('calls onDismiss and hides alert when dismissed', async () => {
    const onDismiss = vi.fn();

    renderErrorAlert({
      message: 'Dismissible alert',
      dismissible: true,
      onDismiss,
    });

    const dismissButton = screen.getByLabelText('Dismiss alert');
    await fireEvent.click(dismissButton);

    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('renders with custom className', () => {
    renderErrorAlert({
      message: 'Custom class alert',
      className: 'custom-alert-class',
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('custom-alert-class');
  });

  it('spreads additional props', () => {
    renderErrorAlert({
      message: 'Alert with id',
      id: 'test-alert',
      'data-testid': 'error-alert',
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('id', 'test-alert');
    expect(alert).toHaveAttribute('data-testid', 'error-alert');
  });
});

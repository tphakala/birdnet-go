import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ErrorAlert from './ErrorAlert.svelte';
import ErrorAlertTestWrapper from './ErrorAlert.test.svelte';

describe('ErrorAlert', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlert as any, {
      props: { message: 'An error occurred' },
    });

    const alert = screen.getByRole('alert');
    expect(alert).toBeInTheDocument();
    expect(alert).toHaveClass('alert', 'alert-error');
    expect(screen.getByText('An error occurred')).toBeInTheDocument();
  });

  it('renders different alert types', () => {
    const types = ['error', 'warning', 'info', 'success'] as const;

    types.forEach(type => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { unmount } = render(ErrorAlert as any, {
        props: {
          message: `${type} message`,
          type,
        },
      });

      const alert = screen.getByRole('alert');
      expect(alert).toHaveClass(`alert-${type}`);

      unmount();
    });
  });

  it('renders with custom children', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlertTestWrapper as any, {
      props: {
        showChildren: true,
      },
    });

    expect(screen.getByText('Custom error content')).toBeInTheDocument();
    expect(screen.getByText('With a link')).toBeInTheDocument();
  });

  it('shows dismiss button when dismissible', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlert as any, {
      props: {
        message: 'Dismissible alert',
        dismissible: true,
      },
    });

    const dismissButton = screen.getByLabelText('Dismiss alert');
    expect(dismissButton).toBeInTheDocument();
  });

  it('calls onDismiss and hides alert when dismissed', async () => {
    const onDismiss = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlert as any, {
      props: {
        message: 'Dismissible alert',
        dismissible: true,
        onDismiss,
      },
    });

    const dismissButton = screen.getByLabelText('Dismiss alert');
    await fireEvent.click(dismissButton);

    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('renders with custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlert as any, {
      props: {
        message: 'Custom class alert',
        className: 'custom-alert-class',
      },
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('custom-alert-class');
  });

  it('spreads additional props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ErrorAlert as any, {
      props: {
        message: 'Alert with id',
        id: 'test-alert',
        'data-testid': 'error-alert',
      },
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('id', 'test-alert');
    expect(alert).toHaveAttribute('data-testid', 'error-alert');
  });
});

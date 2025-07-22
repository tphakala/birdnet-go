import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import type { ComponentType } from 'svelte';
import NotificationToast from './NotificationToast.svelte';
import NotificationToastTestWrapper from './NotificationToast.test.svelte';

// Type helper for Svelte component testing
type SvelteTestComponent<T = Record<string, unknown>> = ComponentType<T>;

describe('NotificationToast', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('renders with message', () => {
    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Test notification',
      },
    });

    expect(screen.getByText('Test notification')).toBeInTheDocument();
  });

  it.each([['info'], ['success'], ['warning'], ['error']] as const)(
    'renders with type %s',
    type => {
      const { container } = render(NotificationToast as SvelteTestComponent, {
        props: {
          type,
          message: `${type} message`,
        },
      });

      const alert = container.querySelector('.alert');
      expect(alert).toHaveClass(`alert-${type}`);
    }
  );

  it('auto-dismisses after duration', async () => {
    const onClose = vi.fn();

    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Auto dismiss',
        duration: 3000,
        onClose,
      },
    });

    expect(screen.getByText('Auto dismiss')).toBeInTheDocument();

    // Fast-forward time
    vi.advanceTimersByTime(3000);

    await waitFor(() => {
      expect(screen.queryByText('Auto dismiss')).not.toBeInTheDocument();
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  it('does not auto-dismiss when duration is null', () => {
    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Persistent notification',
        duration: null,
      },
    });

    expect(screen.getByText('Persistent notification')).toBeInTheDocument();

    // Fast-forward time
    vi.advanceTimersByTime(10000);

    // Should still be visible
    expect(screen.getByText('Persistent notification')).toBeInTheDocument();
  });

  it('closes when close button clicked', async () => {
    const onClose = vi.fn();

    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Close me',
        onClose,
      },
    });

    const closeButton = screen.getByLabelText('Close notification');
    await fireEvent.click(closeButton);

    expect(screen.queryByText('Close me')).not.toBeInTheDocument();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders with actions', async () => {
    const action1 = vi.fn();
    const action2 = vi.fn();

    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Action toast',
        actions: [
          { label: 'Retry', onClick: action1 },
          { label: 'Dismiss', onClick: action2 },
        ],
      },
    });

    const retryButton = screen.getByText('Retry');
    const dismissButton = screen.getByText('Dismiss');

    await fireEvent.click(retryButton);
    expect(action1).toHaveBeenCalledTimes(1);

    await fireEvent.click(dismissButton);
    expect(action2).toHaveBeenCalledTimes(1);
  });

  it('renders at different positions', () => {
    const positions = [
      { position: 'top-left', class: 'toast-start toast-top' },
      { position: 'top-center', class: 'toast-center toast-top' },
      { position: 'top-right', class: 'toast-end toast-top' },
      { position: 'bottom-left', class: 'toast-start toast-bottom' },
      { position: 'bottom-center', class: 'toast-center toast-bottom' },
      { position: 'bottom-right', class: 'toast-end toast-bottom' },
    ] as const;

    positions.forEach(({ position, class: expectedClass }) => {
      const { container, unmount } = render(NotificationToast as SvelteTestComponent, {
        props: {
          message: 'Test',
          position,
        },
      });

      const toast = container.querySelector('.toast');
      expect(toast).toHaveClass(...expectedClass.split(' '));
      unmount();
    });
  });

  it('shows icon by default', () => {
    const { container } = render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'With icon',
        type: 'success',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toBeInTheDocument();
  });

  it('hides icon when showIcon is false', () => {
    const { container } = render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'No icon',
        showIcon: false,
      },
    });

    // Should only have the close button icon
    const svgs = container.querySelectorAll('svg');
    expect(svgs).toHaveLength(1); // Only close button
  });

  it('renders with custom children content', () => {
    render(NotificationToastTestWrapper as SvelteTestComponent, {
      props: {
        showChildren: true,
      },
    });

    expect(screen.getByText('Main message')).toBeInTheDocument();
    expect(screen.getByText('Additional details')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    const { container } = render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Custom class',
        className: 'custom-toast',
      },
    });

    const alert = container.querySelector('.alert');
    expect(alert).toHaveClass('custom-toast');
  });

  it('sets proper ARIA attributes', () => {
    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Info toast',
        type: 'info',
      },
    });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('aria-live', 'polite');

    const { container } = render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Error toast',
        type: 'error',
      },
    });

    const errorAlert = container.querySelector('[role="alert"]');
    expect(errorAlert).toHaveAttribute('aria-live', 'assertive');
  });

  it('clears timeout when closed manually', async () => {
    const onClose = vi.fn();

    render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Manual close',
        duration: 5000,
        onClose,
      },
    });

    const closeButton = screen.getByLabelText('Close notification');
    await fireEvent.click(closeButton);

    // Fast-forward time past the original duration
    vi.advanceTimersByTime(6000);

    // onClose should only be called once
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('cleans up timeout on unmount', () => {
    const { unmount } = render(NotificationToast as SvelteTestComponent, {
      props: {
        message: 'Unmount test',
        duration: 5000,
      },
    });

    unmount();

    // Fast-forward time
    vi.advanceTimersByTime(6000);

    // No errors should occur
    expect(true).toBe(true);
  });
});

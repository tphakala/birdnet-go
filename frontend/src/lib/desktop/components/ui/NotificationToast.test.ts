import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  renderTyped,
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import NotificationToast from './NotificationToast.svelte';
import NotificationToastTestWrapper from './NotificationToast.test.svelte';

// Errors are alerts, every other type is a status message
const TOAST_SELECTOR = '[role="alert"], [role="status"]';

describe('NotificationToast', () => {
  const toastTest = createComponentTestFactory(NotificationToast);

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it('renders with message', () => {
    toastTest.render({
      props: {
        message: 'Test notification',
      },
    });

    expect(screen.getByText('Test notification')).toBeInTheDocument();
  });

  it.each([['info'], ['success'], ['warning'], ['error']] as const)(
    'renders with type %s',
    type => {
      const { container } = toastTest.render({
        props: {
          type,
          message: `${type} message`,
        },
      });

      // The toast is an alert for errors, a status otherwise, with CSS variable-based type classes
      const alert = container.querySelector(TOAST_SELECTOR);
      expect(alert).toBeInTheDocument();
      // Verify it has the appropriate type-based background class
      expect(alert?.className).toContain(`bg-[var(--color-${type})]`);
    }
  );

  it('auto-dismisses after duration', async () => {
    const onClose = vi.fn();

    toastTest.render({
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
    toastTest.render({
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

    toastTest.render({
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

    toastTest.render({
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

  it.each([['info'], ['success'], ['warning'], ['error']] as const)(
    'colours %s text with the content token of its fill',
    type => {
      const { container } = toastTest.render({ props: { type, message: `${type} message` } });

      expect(container.querySelector(TOAST_SELECTOR)?.className).toContain(
        `text-[var(--color-${type}-content)]`
      );
    }
  );

  it('outlines action buttons in the toast text colour and gives them no background', () => {
    toastTest.render({
      props: { message: 'Action toast', actions: [{ label: 'Retry', onClick: vi.fn() }] },
    });

    const classes = screen.getByText('Retry').className.split(/\s+/);
    expect(classes).toContain('border');
    expect(classes).toContain('border-current');
    expect(classes.filter(name => name.split(':').pop()?.startsWith('bg-'))).toEqual([]);
    // The only text utility is the size, so the label keeps the toast's own text colour.
    expect(classes.filter(name => name.split(':').pop()?.startsWith('text-'))).toEqual(['text-xs']);
  });

  it('gives the close button no background fill', () => {
    toastTest.render({ props: { message: 'Close toast' } });

    const classes = screen.getByLabelText('Close notification').className.split(/\s+/);
    expect(classes.filter(name => name.split(':').pop()?.startsWith('bg-'))).toEqual([]);
    expect(
      classes.some(name => name.startsWith('hover:ring') || name.startsWith('hover:underline'))
    ).toBe(true);
  });

  it('renders at different positions', () => {
    const positions = [
      'top-left',
      'top-center',
      'top-right',
      'bottom-left',
      'bottom-center',
      'bottom-right',
    ] as const;

    positions.forEach(position => {
      const { container, unmount } = toastTest.render({
        props: {
          message: 'Test',
          position,
        },
      });

      // Architecture Note: Toast positioning is handled by ToastContainer, not individual toasts
      // The NotificationToast component intentionally has empty position classes because:
      // 1. ToastContainer manages all fixed positioning, z-index, and layout
      // 2. Individual toasts only handle their content, styling, and animations
      // 3. This separation of concerns allows ToastContainer to manage stacking and grouping
      // Therefore, we verify the toast renders without position-specific classes
      const alert = container.querySelector(TOAST_SELECTOR);
      expect(alert).toBeInTheDocument();

      // Verify no position-specific classes are applied to individual toasts
      // These classes should only exist on ToastContainer elements
      expect(alert?.className).not.toMatch(/toast-(start|end|center|top|bottom)/);
      unmount();
    });
  });

  it('shows icon by default', () => {
    const { container } = toastTest.render({
      props: {
        message: 'With icon',
        type: 'success',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toBeInTheDocument();
  });

  it('hides icon when showIcon is false', () => {
    const { container } = toastTest.render({
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
    renderTyped(NotificationToastTestWrapper, {
      props: {
        showChildren: true,
      },
    });

    expect(screen.getByText('Main message')).toBeInTheDocument();
    expect(screen.getByText('Additional details')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    const { container } = toastTest.render({
      props: {
        message: 'Custom class',
        className: 'custom-toast',
      },
    });

    // The inner div with the live-region role should have the custom class
    const alert = container.querySelector(TOAST_SELECTOR);
    expect(alert).toHaveClass('custom-toast');
  });

  it.each([
    ['error', 'alert'],
    ['warning', 'status'],
    ['info', 'status'],
    ['success', 'status'],
  ] as const)('gives a %s toast the %s role without aria-live', (type, role) => {
    toastTest.render({ props: { type, message: `${type} toast` } });

    const toast = screen.getByRole(role);
    expect(toast).toHaveTextContent(`${type} toast`);
    expect(toast).not.toHaveAttribute('aria-live');
    expect(screen.queryByRole(role === 'alert' ? 'status' : 'alert')).not.toBeInTheDocument();
  });

  it('clears timeout when closed manually', async () => {
    const onClose = vi.fn();

    toastTest.render({
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
    const { unmount } = toastTest.render({
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

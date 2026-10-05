import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  renderTyped,
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import Modal from './Modal.svelte';
import ModalTestWrapper from './Modal.test.svelte';

describe('Modal', () => {
  let user: ReturnType<typeof userEvent.setup>;
  const modalTest = createComponentTestFactory(Modal);

  beforeEach(() => {
    user = userEvent.setup();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders when isOpen is true', () => {
    modalTest.render({
      isOpen: true,
      title: 'Test Modal',
    });

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('Test Modal')).toBeInTheDocument();
  });

  it('does not render when isOpen is false', () => {
    modalTest.render({
      props: {
        isOpen: false,
        title: 'Hidden Modal',
      },
    });

    const dialog = screen.getByRole('dialog', { hidden: true });
    expect(dialog).toHaveClass('opacity-0', 'invisible');
    expect(dialog).not.toHaveClass('opacity-100', 'visible');
    expect(screen.queryByText('Hidden Modal')).toBeInTheDocument(); // Still in DOM but hidden
  });

  it('renders with custom content using children snippet', () => {
    renderTyped(ModalTestWrapper, {
      props: {
        isOpen: true,
        showChildren: true,
      },
    });

    expect(screen.getByText('Custom modal content')).toBeInTheDocument();
  });

  it('renders different sizes', () => {
    const sizes = ['sm', 'md', 'lg', 'xl', 'full'] as const;

    sizes.forEach(size => {
      const { unmount } = modalTest.render({
        props: {
          isOpen: true,
          size,
        },
      });

      const modalBox = screen.getByRole('document');
      const expectedClass = size === 'full' ? 'max-w-full' : `max-w-${size}`;
      expect(modalBox).toHaveClass(expectedClass);
      unmount();
    });
  });

  it('shows close button by default', () => {
    modalTest.render({
      props: {
        isOpen: true,
        title: 'Closeable Modal',
      },
    });

    const closeButton = screen.getByLabelText('Close modal');
    expect(closeButton).toBeInTheDocument();
  });

  it('hides close button when showCloseButton is false', () => {
    modalTest.render({
      props: {
        isOpen: true,
        title: 'No Close Button',
        showCloseButton: false,
      },
    });

    expect(screen.queryByLabelText('Close modal')).not.toBeInTheDocument();
  });

  it('calls onClose when close button clicked', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        title: 'Test',
        onClose,
      },
    });

    const closeButton = screen.getByLabelText('Close modal');
    await fireEvent.click(closeButton);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onClose when backdrop clicked and closeOnBackdrop is true', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        title: 'Backdrop Close',
        onClose,
        closeOnBackdrop: true,
      },
    });

    const dialog = screen.getByRole('dialog');
    await fireEvent.click(dialog);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not close on backdrop click when closeOnBackdrop is false', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        title: 'No Backdrop Close',
        onClose,
        closeOnBackdrop: false,
      },
    });

    const dialog = screen.getByRole('dialog');
    await fireEvent.click(dialog);

    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes on Escape key when closeOnEsc is true', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        title: 'Escape Close',
        onClose,
        closeOnEsc: true,
      },
    });

    await user.keyboard('{Escape}');

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not close on Escape when closeOnEsc is false', async () => {
    const onClose = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        title: 'No Escape Close',
        onClose,
        closeOnEsc: false,
      },
    });

    await user.keyboard('{Escape}');

    expect(onClose).not.toHaveBeenCalled();
  });

  it('renders confirm type modal with action buttons', () => {
    modalTest.render({
      props: {
        isOpen: true,
        title: 'Confirm Action',
        type: 'confirm',
      },
    });

    expect(screen.getByText('Cancel')).toBeInTheDocument();
    expect(screen.getByText('Confirm')).toBeInTheDocument();
  });

  it('uses custom button labels', () => {
    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        confirmLabel: 'Delete',
        cancelLabel: 'Keep',
      },
    });

    expect(screen.getByText('Keep')).toBeInTheDocument();
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('calls onConfirm when confirm button clicked', async () => {
    const onConfirm = vi.fn();

    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('handles async onConfirm with loading state', async () => {
    const onConfirm = vi.fn(() => new Promise(resolve => setTimeout(resolve, 100)));

    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    // Should show loading spinner (animate-spin class on the spinner element)
    expect(confirmButton.querySelector('.animate-spin')).toBeInTheDocument();

    // Wait for async operation to complete
    await waitFor(() => {
      expect(confirmButton.querySelector('.animate-spin')).not.toBeInTheDocument();
    });

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('disables buttons during loading', () => {
    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        loading: true,
      },
    });

    const cancelButton = screen.getByText('Cancel');
    const confirmButton = screen.getByText('Confirm');

    expect(cancelButton).toBeDisabled();
    expect(confirmButton).toBeDisabled();
  });

  it('renders with custom header snippet', () => {
    renderTyped(ModalTestWrapper, {
      props: {
        isOpen: true,
        showCustomHeader: true,
      },
    });

    expect(screen.getByText('Custom Header')).toBeInTheDocument();
    expect(screen.getByText('With subtitle')).toBeInTheDocument();
  });

  it('renders with custom footer snippet', () => {
    renderTyped(ModalTestWrapper, {
      props: {
        isOpen: true,
        showCustomFooter: true,
      },
    });

    expect(screen.getByText('Custom Action')).toBeInTheDocument();
  });

  it('applies confirm button variant', () => {
    const variants = [
      'primary',
      'secondary',
      'accent',
      'info',
      'success',
      'warning',
      'error',
    ] as const;

    variants.forEach(variant => {
      const { unmount } = modalTest.render({
        props: {
          isOpen: true,
          type: 'confirm',
          confirmVariant: variant,
        },
      });

      const confirmButton = screen.getByText('Confirm');
      expect(confirmButton).toHaveClass(`bg-[var(--color-${variant})]`);
      unmount();
    });
  });

  it('prevents closing during confirmation', async () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn(() => new Promise(resolve => setTimeout(resolve, 100)));

    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        onClose,
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    // Try to close while confirming
    const cancelButton = screen.getByText('Cancel');
    await fireEvent.click(cancelButton);

    expect(onClose).not.toHaveBeenCalled();

    // Wait for confirmation to complete
    await waitFor(() => {
      expect(confirmButton.querySelector('.animate-spin')).not.toBeInTheDocument();
    });
  });

  it('applies custom className', () => {
    modalTest.render({
      props: {
        isOpen: true,
        className: 'custom-modal-box',
      },
    });

    const modalBox = screen.getByRole('document');
    expect(modalBox).toHaveClass('custom-modal-box');
  });

  it('sets proper ARIA attributes', () => {
    modalTest.render({
      props: {
        isOpen: true,
        title: 'Accessible Modal',
      },
    });

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAttribute('aria-labelledby', 'modal-title');
  });

  describe('initial focus', () => {
    // jsdom cannot compute the open transition, so these tests stand in for it:
    // while `focusBlocked` is set, focus() does nothing, as it does in a browser
    // while the dialog is still hidden by its visibility transition.
    let focusBlocked = false;
    const realFocus = HTMLElement.prototype.focus;

    beforeEach(() => {
      focusBlocked = false;
      vi.useFakeTimers({
        toFake: ['setTimeout', 'clearTimeout', 'requestAnimationFrame', 'cancelAnimationFrame'],
      });
      vi.spyOn(HTMLElement.prototype, 'focus').mockImplementation(function (
        this: HTMLElement,
        options?: FocusOptions
      ) {
        if (!focusBlocked) realFocus.call(this, options);
      });
    });

    afterEach(() => {
      vi.useRealTimers();
      vi.restoreAllMocks();
    });

    const closeButton = () => screen.getByRole('button', { name: 'Close modal' });

    async function openWithTransition() {
      const view = modalTest.render({ props: { isOpen: false, title: 'Focus Modal' } });
      focusBlocked = true;
      await view.rerender({ isOpen: true });
      // The first attempt runs after the open render and fails while hidden
      vi.advanceTimersByTime(0);
      expect(closeButton()).not.toHaveFocus();
      return view;
    }

    it('focuses the first focusable element of a modal created open', () => {
      modalTest.render({ props: { isOpen: true, title: 'Focus Modal' } });

      vi.advanceTimersByTime(0);

      expect(closeButton()).toHaveFocus();
    });

    it('focuses the dialog when its open transition ends', async () => {
      await openWithTransition();

      focusBlocked = false;
      screen.getByRole('dialog').dispatchEvent(new Event('transitionend'));

      expect(closeButton()).toHaveFocus();
    });

    it('retries on animation frames until the dialog can take focus', async () => {
      await openWithTransition();
      vi.advanceTimersToNextFrame();
      vi.advanceTimersToNextFrame();
      expect(closeButton()).not.toHaveFocus();

      focusBlocked = false;
      vi.advanceTimersToNextFrame();

      expect(closeButton()).toHaveFocus();
    });

    it('stops retrying after the bounded wait', async () => {
      await openWithTransition();

      vi.advanceTimersByTime(2000);
      focusBlocked = false;
      vi.advanceTimersToNextFrame();
      screen.getByRole('dialog').dispatchEvent(new Event('transitionend'));

      expect(closeButton()).not.toHaveFocus();
    });

    it('stops retrying when the modal closes before it could take focus', async () => {
      const view = await openWithTransition();

      await view.rerender({ isOpen: false });
      focusBlocked = false;
      vi.advanceTimersToNextFrame();
      screen.getByRole('dialog', { hidden: true }).dispatchEvent(new Event('transitionend'));

      expect(closeButton()).not.toHaveFocus();
    });

    it('leaves focus alone once it is inside the dialog', async () => {
      modalTest.render({ props: { isOpen: true, type: 'confirm', title: 'Focus Modal' } });
      vi.advanceTimersByTime(0);
      const cancel = screen.getByRole('button', { name: 'Cancel' });
      const confirm = screen.getByRole('button', { name: 'Confirm' });
      expect(cancel).toHaveFocus();

      confirm.focus();
      vi.advanceTimersToNextFrame();
      screen.getByRole('dialog').dispatchEvent(new Event('transitionend'));

      expect(confirm).toHaveFocus();
    });
  });
});

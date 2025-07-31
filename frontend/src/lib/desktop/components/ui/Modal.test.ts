import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderTyped, createComponentTestFactory, screen, fireEvent, waitFor } from '../../../../test/render-helpers';
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = modalTest.render({
      props: {
        isOpen: false,
        title: 'Hidden Modal',
      },
    });

    const modal = container.querySelector('.modal');
    expect(modal).not.toHaveClass('modal-open');
    expect(screen.queryByText('Hidden Modal')).toBeInTheDocument(); // Still in DOM but hidden
  });

  it('renders with custom content using children snippet', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { container, unmount } = modalTest.render({
        props: {
          isOpen: true,
          size,
        },
      });

      const modalBox = container.querySelector('.modal-box');
      const expectedClass = size === 'full' ? 'max-w-full' : `max-w-${size}`;
      expect(modalBox).toHaveClass(expectedClass);
      unmount();
    });
  });

  it('shows close button by default', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = modalTest.render({
      props: {
        isOpen: true,
        title: 'Backdrop Close',
        onClose,
        closeOnBackdrop: true,
      },
    });

    const modal = container.querySelector('.modal');
    if (modal) {
      await fireEvent.click(modal);
    }

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not close on backdrop click when closeOnBackdrop is false', async () => {
    const onClose = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = modalTest.render({
      props: {
        isOpen: true,
        title: 'No Backdrop Close',
        onClose,
        closeOnBackdrop: false,
      },
    });

    const modal = container.querySelector('.modal');
    if (modal) {
      await fireEvent.click(modal);
    }

    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes on Escape key when closeOnEsc is true', async () => {
    const onClose = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    modalTest.render({
      props: {
        isOpen: true,
        type: 'confirm',
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    // Should show loading spinner
    expect(confirmButton.querySelector('.loading-spinner')).toBeInTheDocument();

    // Wait for async operation to complete
    await waitFor(() => {
      expect(confirmButton.querySelector('.loading-spinner')).not.toBeInTheDocument();
    });

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('disables buttons during loading', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { unmount } = modalTest.render({
        props: {
          isOpen: true,
          type: 'confirm',
          confirmVariant: variant,
        },
      });

      const confirmButton = screen.getByText('Confirm');
      expect(confirmButton).toHaveClass(`btn-${variant}`);
      unmount();
    });
  });

  it('prevents closing during confirmation', async () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn(() => new Promise(resolve => setTimeout(resolve, 100)));

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
      expect(confirmButton.querySelector('.loading-spinner')).not.toBeInTheDocument();
    });
  });

  it('applies custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = modalTest.render({
      props: {
        isOpen: true,
        className: 'custom-modal-box',
      },
    });

    const modalBox = container.querySelector('.modal-box');
    expect(modalBox).toHaveClass('custom-modal-box');
  });

  it('sets proper ARIA attributes', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
});

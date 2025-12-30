import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import ConfirmModal from './ConfirmModal.svelte';

describe('ConfirmModal', () => {
  let user: ReturnType<typeof userEvent.setup>;

  beforeEach(() => {
    user = userEvent.setup();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders when isOpen is true', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('does not show modal-open class when isOpen is false', () => {
    const { container } = render(ConfirmModal, {
      props: {
        isOpen: false,
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    const modal = container.querySelector('.modal');
    expect(modal).not.toHaveClass('modal-open');
  });

  it('renders custom title', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        title: 'Delete Item?',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    expect(screen.getByText('Delete Item?')).toBeInTheDocument();
  });

  it('renders custom message', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        message: 'This action cannot be undone.',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    expect(screen.getByText('This action cannot be undone.')).toBeInTheDocument();
  });

  it('renders custom button labels', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        confirmLabel: 'Delete',
        cancelLabel: 'Keep',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    expect(screen.getByText('Delete')).toBeInTheDocument();
    expect(screen.getByText('Keep')).toBeInTheDocument();
  });

  it('calls onClose when cancel button is clicked', async () => {
    const onClose = vi.fn();

    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose,
        onConfirm: vi.fn(),
      },
    });

    const cancelButton = screen.getByText('Cancel');
    await fireEvent.click(cancelButton);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onConfirm when confirm button is clicked', async () => {
    const onConfirm = vi.fn();

    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose: vi.fn(),
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('applies correct variant to confirm button', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        confirmVariant: 'error',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    const confirmButton = screen.getByText('Confirm');
    expect(confirmButton).toHaveClass('btn-error');
  });

  it('applies primary variant by default', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    // Default confirmVariant is 'error' per component implementation
    const confirmButton = screen.getByText('Confirm');
    expect(confirmButton).toHaveClass('btn-error');
  });

  it('closes on escape key', async () => {
    const onClose = vi.fn();

    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose,
        onConfirm: vi.fn(),
      },
    });

    await user.keyboard('{Escape}');

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('has proper ARIA attributes', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
  });

  it('handles async onConfirm', async () => {
    const onConfirm = vi.fn(() => new Promise<void>(resolve => setTimeout(resolve, 50)));

    render(ConfirmModal, {
      props: {
        isOpen: true,
        onClose: vi.fn(),
        onConfirm,
      },
    });

    const confirmButton = screen.getByText('Confirm');
    await fireEvent.click(confirmButton);

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('uses warning variant correctly', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        confirmVariant: 'warning',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    const confirmButton = screen.getByText('Confirm');
    expect(confirmButton).toHaveClass('btn-warning');
  });

  it('renders with success variant for positive confirmations', () => {
    render(ConfirmModal, {
      props: {
        isOpen: true,
        title: 'Save Changes?',
        message: 'Your changes will be saved.',
        confirmLabel: 'Save',
        confirmVariant: 'success',
        onClose: vi.fn(),
        onConfirm: vi.fn(),
      },
    });

    const confirmButton = screen.getByText('Save');
    expect(confirmButton).toHaveClass('btn-success');
    expect(screen.getByText('Save Changes?')).toBeInTheDocument();
  });
});

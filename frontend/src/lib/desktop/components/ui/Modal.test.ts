import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  renderTyped,
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'svelte';
import Modal from './Modal.svelte';
import ModalTestWrapper from './Modal.test.svelte';
import ModalTrapHost from './Modal.trap.test.svelte';
import ModalStackHost from './Modal.stack.test.svelte';
import ModalCardsHost from './Modal.cards.test.svelte';

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

    expect(screen.getByRole('dialog')).toHaveAttribute('aria-modal', 'true');
  });

  describe('labelling', () => {
    const stackTest = createComponentTestFactory(ModalStackHost);

    it('names the dialog by its title', () => {
      modalTest.render({ props: { isOpen: true, title: 'Accessible Modal' } });

      expect(screen.getByRole('dialog', { name: 'Accessible Modal' })).toBeInTheDocument();
    });

    it('points aria-labelledby at its own title element', () => {
      modalTest.render({ props: { isOpen: true, title: 'Accessible Modal' } });

      const dialog = screen.getByRole('dialog');
      const titleId = dialog.getAttribute('aria-labelledby');
      expect(titleId).toBeTruthy();
      expect(document.getElementById(titleId as string)).toHaveTextContent('Accessible Modal');
      expect(titleId).not.toBe('modal-title');
    });

    it('two open modals have distinct title and body ids', () => {
      stackTest.render({ props: { firstOpen: true, secondOpen: true, describeBody: true } });

      const lower = screen.getByRole('dialog', { name: 'Lower modal' });
      const upper = screen.getByRole('dialog', { name: 'Upper modal' });
      expect(lower.getAttribute('aria-labelledby')).not.toBe(upper.getAttribute('aria-labelledby'));
      expect(lower.getAttribute('aria-describedby')).not.toBe(
        upper.getAttribute('aria-describedby')
      );
      expect(lower).toHaveAccessibleDescription('Lower action');
      expect(upper).toHaveAccessibleDescription('Upper action');
    });

    it('keeps its ids across close and reopen', async () => {
      const view = stackTest.render({ props: { firstOpen: true, describeBody: true } });
      const dialog = screen.getByRole('dialog', { name: 'Lower modal' });
      const before = [
        dialog.getAttribute('aria-labelledby'),
        dialog.getAttribute('aria-describedby'),
      ];

      await view.rerender({ firstOpen: false, describeBody: true });
      await view.rerender({ firstOpen: true, describeBody: true });

      expect([
        dialog.getAttribute('aria-labelledby'),
        dialog.getAttribute('aria-describedby'),
      ]).toEqual(before);
    });

    it('does not describe the dialog by default', () => {
      renderTyped(ModalTestWrapper, { props: { isOpen: true, showChildren: true } });

      expect(screen.getByRole('dialog')).not.toHaveAttribute('aria-describedby');
    });

    it('describes the dialog by its body when describeBody is set', () => {
      stackTest.render({ props: { firstOpen: true, describeBody: true } });

      expect(screen.getByRole('dialog', { name: 'Lower modal' })).toHaveAccessibleDescription(
        'Lower action'
      );
    });

    it('a header snippet receives the title id', () => {
      renderTyped(ModalTestWrapper, { props: { isOpen: true, showCustomHeader: true } });

      expect(screen.getByRole('dialog', { name: 'Custom Header' })).toBeInTheDocument();
    });
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
  describe('focus trap', () => {
    const button = (name: string) => screen.getByRole('button', { name });
    const trapHostTest = createComponentTestFactory(ModalTrapHost);
    const stackHostTest = createComponentTestFactory(ModalStackHost);

    /** Renders the trap host and waits until initial focus is inside the dialog. */
    async function renderHost(props: ComponentProps<typeof ModalTrapHost> = {}) {
      const view = trapHostTest.render({ props });
      await waitFor(() => {
        expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
      });
      return view;
    }

    it('Tab from the last enabled control wraps to the first when the last button is disabled', async () => {
      await renderHost({ lastDisabled: true });
      button('Middle').focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Shift+Tab from the first enabled control wraps to the last when the first is disabled', async () => {
      renderTyped(ModalTrapHost, { props: { firstDisabled: true } });
      button('Middle').focus();

      await user.tab({ shift: true });

      expect(button('Last')).toHaveFocus();
    });

    it('a control enabled after opening joins the trap', async () => {
      const view = await renderHost({ lastDisabled: true });
      button('Middle').focus();

      await view.rerender({ lastDisabled: false });
      await user.tab();
      expect(button('Last')).toHaveFocus();

      await user.tab();
      expect(button('First')).toHaveFocus();
    });

    it('a control added after opening joins the trap', async () => {
      const view = await renderHost();
      button('Last').focus();

      await view.rerender({ showExtra: true });
      await user.tab();
      expect(button('Extra')).toHaveFocus();

      await user.tab();
      expect(button('First')).toHaveFocus();
    });

    it('Tab with focus on body moves focus to the first control', async () => {
      await renderHost();
      (document.activeElement as HTMLElement).blur();
      expect(document.body).toHaveFocus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Shift+Tab with focus on body moves focus to the last control', async () => {
      await renderHost();
      (document.activeElement as HTMLElement).blur();
      expect(document.body).toHaveFocus();

      await user.tab({ shift: true });

      expect(button('Last')).toHaveFocus();
    });

    it('Tab with focus outside the dialog moves focus back into it', async () => {
      await renderHost();
      button('Outside after').focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Tab with focus in another aria-modal dialog leaves focus there', async () => {
      await renderHost();
      const other = document.createElement('div');
      other.setAttribute('role', 'dialog');
      other.setAttribute('aria-modal', 'true');
      const otherInput = document.createElement('input');
      other.append(otherInput);
      document.body.append(other);
      try {
        otherInput.focus();

        // fireEvent returns false when a listener called preventDefault
        const notPrevented = await fireEvent.keyDown(otherInput, { key: 'Tab' });

        expect(notPrevented).toBe(true);
        expect(otherInput).toHaveFocus();
      } finally {
        other.remove();
      }
    });

    it('Tab with focus on SVG content in another aria-modal dialog leaves focus there', async () => {
      await renderHost();
      const other = document.createElement('div');
      other.setAttribute('role', 'dialog');
      other.setAttribute('aria-modal', 'true');
      const chart = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      chart.setAttribute('tabindex', '0');
      other.append(chart);
      document.body.append(other);
      try {
        chart.focus();

        const notPrevented = await fireEvent.keyDown(chart, { key: 'Tab' });

        expect(notPrevented).toBe(true);
        expect(chart).toHaveFocus();
      } finally {
        other.remove();
      }
    });

    it('skips tabindex=-1 buttons when wrapping', async () => {
      await renderHost({ lastTabindex: -1 });
      button('Middle').focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Shift+Tab from the checked radio of a leading radio group wraps to the last control', async () => {
      await renderHost({ showRadios: true });
      const checked = screen.getByRole('radio', { name: 'Mode B' });
      checked.focus();

      await user.tab({ shift: true });

      expect(button('Last')).toHaveFocus();
    });

    it('Tab from a tabindex=-1 element inside the dialog follows native order when controls follow it', async () => {
      await renderHost({ showCard: true });
      screen.getByRole('group', { name: 'Card' }).focus();

      await user.tab();

      expect(button('Middle')).toHaveFocus();
    });

    it('Tab from a tabindex=-1 element inside the dialog wraps when nothing follows it', async () => {
      await renderHost({ showTrailingCard: true });
      screen.getByRole('group', { name: 'Trailing card' }).focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('initial focus skips a disabled first control', async () => {
      await renderHost({ firstDisabled: true });

      expect(button('Middle')).toHaveFocus();
    });

    it('initial focus goes to the dialog box when every control is disabled', async () => {
      modalTest.render({ props: { isOpen: true, type: 'confirm', loading: true, title: 'Busy' } });

      await waitFor(() => {
        expect(screen.getByRole('document')).toHaveFocus();
      });
    });

    it('leaves native Tab order alone around a roving card group with focus on an unchecked card', async () => {
      renderTyped(ModalCardsHost, {});
      await waitFor(() => {
        expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
      });
      const cardB = screen.getByRole('radio', { name: 'Card B' });
      const cardA = screen.getByRole('radio', { name: 'Card A' });
      cardB.focus();

      await user.tab();
      expect(button('Next')).toHaveFocus();

      await user.tab({ shift: true });
      expect(cardA).toHaveFocus();
    });

    it('only the topmost of two open modals handles Tab', async () => {
      const view = stackHostTest.render({ props: { firstOpen: true, secondOpen: false } });
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      await view.rerender({ firstOpen: true, secondOpen: true });
      await waitFor(() => expect(button('Upper action')).toHaveFocus());
      (document.activeElement as HTMLElement).blur();

      await user.tab();
      expect(button('Upper action')).toHaveFocus();

      await view.rerender({ firstOpen: true, secondOpen: false });
      (document.activeElement as HTMLElement).blur();
      await user.tab();
      expect(button('Lower action')).toHaveFocus();
    });
  });
  describe('Escape', () => {
    it('does not close on an Escape that a control inside already handled', async () => {
      const onClose = vi.fn();
      const host = createComponentTestFactory(ModalTrapHost);
      host.render({ props: { escapeHandled: true, onClose } });
      const field = screen.getByRole('textbox', { name: 'Handles Escape' });
      field.focus();

      await user.keyboard('{Escape}');

      expect(onClose).not.toHaveBeenCalled();
    });

    it('closes on an Escape nothing inside handled', async () => {
      const onClose = vi.fn();
      const host = createComponentTestFactory(ModalTrapHost);
      host.render({ props: { onClose } });
      screen.getByRole('button', { name: 'Middle' }).focus();

      await user.keyboard('{Escape}');

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('only the topmost of two open modals closes on Escape', async () => {
      const onCloseFirst = vi.fn();
      const onCloseSecond = vi.fn();
      const host = createComponentTestFactory(ModalStackHost);
      const view = host.render({
        props: { firstOpen: true, secondOpen: false, onCloseFirst, onCloseSecond },
      });
      await view.rerender({ firstOpen: true, secondOpen: true, onCloseFirst, onCloseSecond });

      await user.keyboard('{Escape}');

      expect(onCloseSecond).toHaveBeenCalledTimes(1);
      expect(onCloseFirst).not.toHaveBeenCalled();
    });
  });
});

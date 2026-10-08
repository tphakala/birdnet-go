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
import ModalToggleHost from './Modal.toggle.test.svelte';
import ModalSelectHost from './Modal.select.test.svelte';
import ModalCardsHost from './Modal.cards.test.svelte';

function blurActiveElement() {
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
}

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

  describe('scrollBody', () => {
    const renderHost = (props: { scrollBody?: boolean; size?: 'md' | 'full' }) =>
      renderTyped(ModalTestWrapper, {
        props: {
          isOpen: true,
          showChildren: true,
          showCustomHeader: true,
          showCustomFooter: true,
          ...props,
        },
      });

    const bodyOf = () => {
      const body = screen
        .getByText('Custom modal content')
        .closest<HTMLElement>('[id^="modal-body"]');
      if (!body) throw new Error('modal body not found');
      return body;
    };

    it('scrollBody keeps the header and footer outside the scrolling body', () => {
      renderHost({ scrollBody: true });

      const panel = screen.getByRole('document');
      expect(panel).toHaveClass('flex', 'flex-col', 'overflow-hidden');
      expect(panel).not.toHaveClass('overflow-y-auto');
      expect(bodyOf()).toHaveClass('overflow-y-auto', 'min-h-0');
      expect(bodyOf()).not.toContainElement(screen.getByText('Custom Header'));
      expect(bodyOf()).not.toContainElement(screen.getByText('Custom Action'));
      expect(screen.getByText('Custom Header').closest('.shrink-0')).not.toBeNull();
      expect(screen.getByText('Custom Action').parentElement).toHaveClass('shrink-0');
    });

    it('without scrollBody the whole panel scrolls as before', () => {
      renderHost({});

      const panel = screen.getByRole('document');
      expect(panel).toHaveClass('overflow-y-auto');
      expect(panel).not.toHaveClass('overflow-hidden');
      expect(panel).not.toHaveClass('flex-col');
      expect(bodyOf().className).toBe('py-4');
      expect(screen.getByText('Custom Action').parentElement?.className).toBe(
        'flex justify-end gap-2 mt-6'
      );
      // The header renders directly in the panel, without a wrapper
      expect(screen.getByText('Custom Header').parentElement?.parentElement).toBe(panel);
    });

    it('scrollBody keeps the built-in confirm footer and a title-only header outside the body', () => {
      modalTest.render({
        props: {
          isOpen: true,
          scrollBody: true,
          type: 'confirm',
          title: 'Only a title',
          confirmLabel: 'Go ahead',
        },
      });

      expect(screen.getByText('Only a title').parentElement).toHaveClass('shrink-0');
      expect(screen.getByRole('button', { name: 'Go ahead' }).parentElement).toHaveClass(
        'shrink-0'
      );
    });

    it('without scrollBody the built-in confirm footer and title-only header are not wrapped', () => {
      modalTest.render({
        props: { isOpen: true, type: 'confirm', title: 'Only a title', confirmLabel: 'Go ahead' },
      });

      expect(screen.getByText('Only a title').parentElement).toBe(screen.getByRole('document'));
      expect(screen.getByRole('button', { name: 'Go ahead' }).parentElement?.className).toBe(
        'flex justify-end gap-2 mt-6'
      );
    });

    it('keeps the full size width with and without scrollBody', () => {
      const { unmount } = renderHost({ size: 'full' });
      expect(screen.getByRole('document')).toHaveClass('max-w-full', 'w-full');
      unmount();

      renderHost({ size: 'full', scrollBody: true });
      expect(screen.getByRole('document')).toHaveClass('max-w-full', 'w-full');
    });
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
      expect(document.getElementById(titleId ?? '')).toHaveTextContent('Accessible Modal');
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

      // The harness rerender re-runs the open effect, which restores focus; place it after
      await view.rerender({ lastDisabled: false });
      button('Middle').focus();
      await user.tab();
      expect(button('Last')).toHaveFocus();

      await user.tab();
      expect(button('First')).toHaveFocus();
    });

    it('a control added after opening joins the trap', async () => {
      const view = await renderHost();

      await view.rerender({ showExtra: true });
      button('Last').focus();
      await user.tab();
      expect(button('Extra')).toHaveFocus();

      await user.tab();
      expect(button('First')).toHaveFocus();
    });

    it('Tab with focus on body moves focus to the first control', async () => {
      await renderHost();
      blurActiveElement();
      expect(document.body).toHaveFocus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Shift+Tab with focus on body moves focus to the last control', async () => {
      await renderHost();
      blurActiveElement();
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

    it('Tab with focus in a dialog placed inside its own dialog element leaves focus there', async () => {
      await renderHost();
      // An expanded map portalled into the Modal's dialog element, for example
      const other = document.createElement('div');
      other.setAttribute('role', 'dialog');
      other.setAttribute('aria-modal', 'true');
      const field = document.createElement('input');
      other.append(field);
      screen.getByRole('dialog', { name: 'Trap Modal' }).append(other);
      try {
        field.focus();

        const notPrevented = await fireEvent.keyDown(field, { key: 'Tab' });

        expect(notPrevented).toBe(true);
        expect(field).toHaveFocus();
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

    it('Tab from the checked radio reaches a control placed between two radios of the same group', async () => {
      await renderHost({ radioLayout: 'split', hideFooter: true });
      screen.getByRole('radio', { name: 'Split A' }).focus();

      await user.tab();

      expect(button('Between')).toHaveFocus();
    });

    it('Tab from the first radio of an unchecked trailing group wraps to the first control', async () => {
      await renderHost({ radioLayout: 'trailing-unchecked', hideFooter: true });
      screen.getByRole('radio', { name: 'Size X' }).focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Shift+Tab from the last radio of an unchecked leading group wraps to the last control, the button after the group', async () => {
      await renderHost({ radioLayout: 'leading-unchecked', hideFooter: true });
      screen.getByRole('radio', { name: 'Pick Q' }).focus();

      await user.tab({ shift: true });

      expect(button('First')).toHaveFocus();
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

    it.each([
      ['a hidden attribute', 'hidden-attribute'],
      ['an inert subtree', 'inert'],
      ['a hidden input', 'hidden-input'],
    ] as const)('Tab wraps past a last control with %s', async (_label, skippedLast) => {
      await renderHost({ skippedLast });
      button('Last').focus();

      await user.tab();

      expect(button('First')).toHaveFocus();
    });

    it('Tab keeps focus on the dialog box when no control is enabled', async () => {
      modalTest.render({ props: { isOpen: true, type: 'confirm', loading: true, title: 'Busy' } });
      const box = screen.getByRole('document');
      await waitFor(() => expect(box).toHaveFocus());

      await user.tab();

      expect(box).toHaveFocus();
    });

    it('Tab moves focus from body to the dialog box when no control is enabled', async () => {
      modalTest.render({ props: { isOpen: true, type: 'confirm', loading: true, title: 'Busy' } });
      const box = screen.getByRole('document');
      await waitFor(() => expect(box).toHaveFocus());
      blurActiveElement();
      expect(document.body).toHaveFocus();

      await user.tab();

      expect(box).toHaveFocus();
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

    describe.each(['visibilityProperty', 'checkVisibilityCSS'] as const)(
      'with an engine whose checkVisibility knows only %s',
      knownOption => {
        // jsdom has no checkVisibility. This stand-in reports a node inside a
        // [data-vis-hidden] element as hidden only when asked to consider the
        // visibility property under the one option name this engine knows, as
        // Chromium before 121 (checkVisibilityCSS) and later versions
        // (visibilityProperty) do for visibility:hidden.
        beforeEach(() => {
          Object.defineProperty(HTMLElement.prototype, 'checkVisibility', {
            configurable: true,
            writable: true,
            value(
              this: HTMLElement,
              options?: { visibilityProperty?: boolean; checkVisibilityCSS?: boolean }
            ) {
              const considersVisibility =
                knownOption === 'visibilityProperty'
                  ? options?.visibilityProperty
                  : options?.checkVisibilityCSS;
              return !(considersVisibility && this.closest('[data-vis-hidden]'));
            },
          });
        });

        afterEach(() => {
          Reflect.deleteProperty(HTMLElement.prototype, 'checkVisibility');
        });

        it('Tab wraps past a visibility-hidden last control', async () => {
          await renderHost({ visibilityHiddenLast: true });
          button('Last').focus();

          await user.tab();

          expect(button('First')).toHaveFocus();
        });

        it('uses the display-only check while the dialog itself is still hidden, so the trap does not wrap', async () => {
          await renderHost({ visibilityHiddenLast: true, dialogHidden: true });
          button('Last').focus();

          // The control that only the visibility property hides still counts as a next stop,
          // so the trap leaves the key alone (fireEvent returns false when it was prevented)
          const notPrevented = await fireEvent.keyDown(button('Last'), { key: 'Tab' });

          expect(notPrevented).toBe(true);
        });
      }
    );

    it('only the topmost of two open modals handles Tab', async () => {
      const view = stackHostTest.render({ props: { firstOpen: true, secondOpen: false } });
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      await view.rerender({ firstOpen: true, secondOpen: true });
      await waitFor(() => expect(button('Upper action')).toHaveFocus());
      blurActiveElement();

      await user.tab();
      expect(button('Upper action')).toHaveFocus();

      await view.rerender({ firstOpen: true, secondOpen: false });
      blurActiveElement();
      await user.tab();
      expect(button('Lower action')).toHaveFocus();
    });
  });

  describe('focus restore', () => {
    const button = (name: string) => screen.getByRole('button', { name });
    const press = (name: string) => fireEvent.click(button(name));

    async function openBoth() {
      renderTyped(ModalToggleHost, {});
      button('Page button').focus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      await press('Toggle upper');
      await waitFor(() => expect(button('Upper action')).toHaveFocus());
    }

    it('closing the lower of two open modals leaves focus in the upper one', async () => {
      await openBoth();

      await press('Toggle lower');

      expect(button('Upper action')).toHaveFocus();
    });

    it('closing both modals in one update returns focus to the element focused before the first', async () => {
      await openBoth();

      await press('Close both');

      expect(button('Page button')).toHaveFocus();
    });

    it('closing the upper modal opened from the page behind moves focus into the lower one, not behind it', async () => {
      renderTyped(ModalToggleHost, {});
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      // The upper dialog is opened from a control outside the lower one
      button('Toggle upper').focus();
      await press('Toggle upper');
      await waitFor(() => expect(button('Upper action')).toHaveFocus());

      await press('Toggle upper');

      expect(button('Lower action')).toHaveFocus();
    });

    it('closing the lower modal while focus is on its control moves focus into the upper one', async () => {
      await openBoth();
      button('Lower action').focus();

      await press('Toggle lower');

      expect(button('Upper action')).toHaveFocus();
    });

    it('Tab with focus in a lower modal moves focus into the topmost one', async () => {
      await openBoth();
      button('Lower action').focus();

      // fireEvent returns false when a listener called preventDefault
      const notPrevented = await fireEvent.keyDown(button('Lower action'), { key: 'Tab' });

      expect(notPrevented).toBe(false);
      expect(button('Upper action')).toHaveFocus();
    });

    it('closing the upper modal returns focus to the control it was opened from', async () => {
      await openBoth();

      await press('Toggle upper');

      expect(button('Lower action')).toHaveFocus();
    });

    it('leaves no control of the closed dialog focused when the element it was opened from is gone', async () => {
      renderTyped(ModalToggleHost, {});
      const opener = document.createElement('button');
      document.body.append(opener);
      opener.focus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      opener.remove();

      await press('Toggle lower');

      expect(document.body).toHaveFocus();
    });

    it('leaves no control of the closed dialog focused when focus was on body at open', async () => {
      renderTyped(ModalToggleHost, {});
      expect(document.body).toHaveFocus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());

      await press('Toggle lower');

      expect(document.body).toHaveFocus();
    });

    it('does not take its own control as the element it was opened from', async () => {
      renderTyped(ModalToggleHost, {});
      // The closed dialog stays in the page; focus can sit on one of its controls
      screen.getByRole('button', { name: 'Lower action', hidden: true }).focus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());

      await press('Toggle lower');

      expect(document.body).toHaveFocus();
    });

    it('does not move focus when the element it was opened from is gone', async () => {
      renderTyped(ModalToggleHost, {});
      const opener = document.createElement('button');
      document.body.append(opener);
      opener.focus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());
      const focusSpy = vi.spyOn(opener, 'focus');
      opener.remove();

      await press('Toggle lower');

      expect(focusSpy).not.toHaveBeenCalled();
    });

    it('returns focus to focusable SVG content it was opened from', async () => {
      renderTyped(ModalToggleHost, {});
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      const bar = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
      bar.setAttribute('tabindex', '0');
      svg.append(bar);
      document.body.append(svg);
      bar.focus();
      await press('Toggle lower');
      await waitFor(() => expect(button('Lower action')).toHaveFocus());

      await press('Toggle lower');

      expect(bar).toHaveFocus();
      svg.remove();
    });
  });

  describe('with a searchable dropdown inside', () => {
    async function openDropdown(props: { dropdownLast?: boolean } = {}) {
      renderTyped(ModalSelectHost, { props });
      await waitFor(() =>
        expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true)
      );
      await user.click(screen.getByRole('combobox', { name: /Choice/ }));
      const search = await screen.findByRole('searchbox');
      await waitFor(() => expect(search).toHaveFocus());
      return search;
    }

    it('Tab from the search box closes the list and moves on from the dropdown, not to the first control', async () => {
      await openDropdown();

      await user.tab();

      expect(screen.getByRole('button', { name: 'After' })).toHaveFocus();
    });

    it('Shift+Tab from the search box moves back from the dropdown, not to the last control', async () => {
      await openDropdown();

      await user.tab({ shift: true });

      expect(screen.getByRole('button', { name: 'Before' })).toHaveFocus();
    });

    it('Tab from the search box of the last control wraps to the first control', async () => {
      await openDropdown({ dropdownLast: true });

      await user.tab();

      expect(screen.getByRole('button', { name: 'Before' })).toHaveFocus();
    });
  });

  describe('Escape with another aria-modal dialog above', () => {
    function addOtherDialog(parent: HTMLElement) {
      const other = document.createElement('div');
      other.setAttribute('role', 'dialog');
      other.setAttribute('aria-modal', 'true');
      const field = document.createElement('input');
      other.append(field);
      parent.append(other);
      return { other, field };
    }

    it('does not close on an Escape from a dialog placed inside its own dialog element', async () => {
      const onClose = vi.fn();
      const host = createComponentTestFactory(ModalTrapHost);
      host.render({ props: { onClose } });
      // An expanded map portalled into the Modal's dialog element, for example
      const { other, field } = addOtherDialog(screen.getByRole('dialog', { name: 'Trap Modal' }));
      try {
        field.focus();

        await fireEvent.keyDown(field, { key: 'Escape' });

        expect(onClose).not.toHaveBeenCalled();
      } finally {
        other.remove();
      }
    });

    it('does not close on an Escape from a dialog elsewhere in the page', async () => {
      const onClose = vi.fn();
      const host = createComponentTestFactory(ModalTrapHost);
      host.render({ props: { onClose } });
      const { other, field } = addOtherDialog(document.body);
      try {
        field.focus();

        await fireEvent.keyDown(field, { key: 'Escape' });

        expect(onClose).not.toHaveBeenCalled();
      } finally {
        other.remove();
      }
    });

    it('still closes on an Escape from a control of its own dialog', async () => {
      const onClose = vi.fn();
      const host = createComponentTestFactory(ModalTrapHost);
      host.render({ props: { onClose } });
      screen.getByRole('button', { name: 'Middle' }).focus();

      await user.keyboard('{Escape}');

      expect(onClose).toHaveBeenCalledTimes(1);
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

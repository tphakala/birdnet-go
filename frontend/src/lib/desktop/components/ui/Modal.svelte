<script module lang="ts">
  // Open Modal instances, oldest first. Only the last one (the topmost) acts on
  // Tab and Escape, so a dialog opened over another (the setup wizard and its
  // leave confirmation) does not have its keys handled twice.
  const openModals: object[] = [];

  // CSS selector for elements that may be keyboard focusable. getTabbable()
  // narrows the matches to the ones the Tab key actually reaches.
  const TABBABLE_CANDIDATE_SELECTOR =
    'button, a[href], area[href], input, select, textarea, summary, [tabindex]';

  // Another modal dialog (LoginModal, the range filter dialog) that owns focus while open
  const ARIA_MODAL_SELECTOR = '[aria-modal="true"]';

  // Ancestors that take their whole subtree out of the tab order
  const TAB_EXCLUDING_ANCESTOR_SELECTOR = '[inert], [hidden]';

  // Stable number per form element, so a radio group key can include its form
  const formNumbers = new WeakMap<object, number>();
  let formNumberCount = 0;

  /**
   * Radio buttons that share a name and form are one Tab stop in browsers.
   * Returns null for a radio without a name, which is a stop of its own.
   */
  function radioGroupKey(radio: HTMLInputElement): string | null {
    if (!radio.name) return null;
    let formNumber = 0;
    if (radio.form) {
      formNumber = formNumbers.get(radio.form) ?? ++formNumberCount;
      formNumbers.set(radio.form, formNumber);
    }
    return `${formNumber}:${radio.name}`;
  }

  function isRadio(el: HTMLElement): el is HTMLInputElement {
    return el instanceof HTMLInputElement && el.type === 'radio';
  }

  /**
   * Elements inside root that the Tab key can reach, in document order: not
   * disabled, tabindex not negative, not a hidden input, not inside an inert or
   * hidden subtree, and not removed by display:none. Radio buttons are listed as
   * the browser tabs them, see collapseRadioGroups().
   */
  function getTabbable(root: HTMLElement): HTMLElement[] {
    return collapseRadioGroups(getTabbableUncollapsed(root));
  }

  function getTabbableUncollapsed(root: HTMLElement): HTMLElement[] {
    return Array.from(root.querySelectorAll<HTMLElement>(TABBABLE_CANDIDATE_SELECTOR)).filter(
      el => {
        if (el.matches(':disabled')) return false;
        if (el.tabIndex < 0) return false;
        if (el instanceof HTMLInputElement && el.type === 'hidden') return false;
        if (el.closest(TAB_EXCLUDING_ANCESTOR_SELECTOR)) return false;
        // Default options only: display-based. visibilityProperty would drop
        // everything while the dialog is visibility:hidden during its first
        // open frame. jsdom has no checkVisibility, so tests see all candidates.
        if (typeof el.checkVisibility === 'function' && !el.checkVisibility()) return false;
        return true;
      }
    );
  }

  /**
   * A named radio group is one Tab stop: Tab enters it at the checked radio (at
   * the first, or the last with Shift+Tab, when none is checked) and leaves it
   * from any radio. Keeps those stops and drops the other radios of each group.
   */
  function collapseRadioGroups(items: HTMLElement[]): HTMLElement[] {
    const groups = new Map<string, HTMLInputElement[]>();
    for (const el of items) {
      if (!isRadio(el)) continue;
      const key = radioGroupKey(el);
      if (key === null) continue;
      groups.set(key, [...(groups.get(key) ?? []), el]);
    }
    const stops = new Set<HTMLElement>();
    for (const members of groups.values()) {
      const checked = members.find(radio => radio.checked);
      if (checked) {
        stops.add(checked);
      } else {
        stops.add(members[0]);
        stops.add(members[members.length - 1]);
      }
    }
    return items.filter(el => {
      if (!isRadio(el) || radioGroupKey(el) === null) return true;
      return stops.has(el);
    });
  }

  /**
   * The element Tab leaves from when the focused element is `active`: the
   * element itself, except that a named radio group is left from its last radio
   * (first radio with Shift+Tab).
   */
  function tabExitPoint(
    active: HTMLElement,
    tabbable: HTMLElement[],
    backwards: boolean
  ): HTMLElement {
    if (!isRadio(active)) return active;
    const key = radioGroupKey(active);
    if (key === null) return active;
    const members = tabbable.filter(el => isRadio(el) && radioGroupKey(el) === key);
    return (backwards ? members[0] : members[members.length - 1]) ?? active;
  }
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { untrack, type Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { generateId } from '$lib/utils/uuid';

  const logger = loggers.ui;

  // How long initial focus keeps retrying while the dialog cannot take focus yet.
  // The open transition hides the dialog (visibility) for its first frame, and
  // focus() on a hidden element does nothing; 200 ms is the transition, the rest
  // is headroom for slow devices.
  const INITIAL_FOCUS_MAX_WAIT_MS = 1000;

  type ModalSize =
    'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl' | '4xl' | '5xl' | '6xl' | '7xl' | 'full';
  type ModalType = 'default' | 'confirm' | 'alert';

  interface Props extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
    isOpen: boolean;
    title?: string;
    size?: ModalSize;
    type?: ModalType;
    confirmLabel?: string;
    cancelLabel?: string;
    confirmVariant?: 'primary' | 'secondary' | 'accent' | 'info' | 'success' | 'warning' | 'error';
    closeOnBackdrop?: boolean;
    closeOnEsc?: boolean;
    showCloseButton?: boolean;
    loading?: boolean;
    className?: string;
    onClose?: () => void;
    onConfirm?: () => void | Promise<void>;
    /**
     * Describe the dialog by its body content (aria-describedby). Off by default
     * because a long or structured body makes a poor description; turn it on
     * when the body is the short question or message being confirmed.
     */
    describeBody?: boolean;
    /**
     * Replaces the title heading. The dialog is named from `title` through
     * aria-labelledby, which points at `titleId`, so a heading in this snippet
     * must carry `id={titleId}` and `title` must be set; without `title`, pass
     * `aria-labelledby` yourself.
     */
    header?: Snippet<[{ titleId: string }]>;
    children?: Snippet;
    footer?: Snippet;
  }

  let {
    isOpen = false,
    title,
    size = 'md',
    type = 'default',
    confirmLabel = t('common.buttons.confirm'),
    cancelLabel = t('common.buttons.cancel'),
    confirmVariant = 'primary',
    closeOnBackdrop = true,
    closeOnEsc = true,
    showCloseButton = true,
    loading = false,
    describeBody = false,
    className = '',
    onClose,
    onConfirm,
    header,
    children,
    footer,
    ...rest
  }: Props = $props();

  let isConfirming = $state(false);
  let modalElement = $state<HTMLDivElement>();
  let dialogElement = $state<HTMLDivElement>();
  let previousActiveElement: HTMLElement | null = null;
  // Identity of this instance in the openModals stack
  const stackToken = {};
  // Ids are per instance and fixed for its life: the dialog is always mounted,
  // so a shared literal would repeat on every page that has two Modals
  const titleId = generateId('modal-title');
  const bodyId = generateId('modal-body');

  function isTopmost(): boolean {
    return openModals.at(-1) === stackToken;
  }

  const modalBoxBase =
    'bg-[var(--color-base-100)] rounded-[var(--radius-box)] p-6 max-h-[calc(100vh-2rem)] overflow-y-auto shadow-xl relative scale-95 transition-transform duration-200 ease-out';

  const sizeClasses: Record<ModalSize, string> = {
    sm: `${modalBoxBase} max-w-sm`,
    md: `${modalBoxBase} max-w-md`,
    lg: `${modalBoxBase} max-w-lg`,
    xl: `${modalBoxBase} max-w-xl`,
    '2xl': `${modalBoxBase} max-w-2xl`,
    '3xl': `${modalBoxBase} max-w-3xl`,
    '4xl': `${modalBoxBase} max-w-4xl`,
    '5xl': `${modalBoxBase} max-w-5xl`,
    '6xl': `${modalBoxBase} max-w-6xl`,
    '7xl': `${modalBoxBase} max-w-7xl`,
    full: `${modalBoxBase} max-w-full w-full`,
  };

  const confirmButtonStyles: Record<typeof confirmVariant, string> = {
    primary:
      'bg-[var(--color-primary)] text-[var(--color-primary-content)] border-[var(--color-primary)] hover:not-disabled:bg-[var(--color-primary-hover)] hover:not-disabled:border-[var(--color-primary-hover)]',
    secondary:
      'bg-[var(--color-secondary)] text-[var(--color-secondary-content)] border-[var(--color-secondary)] hover:not-disabled:bg-[var(--color-secondary-hover)] hover:not-disabled:border-[var(--color-secondary-hover)]',
    accent:
      'bg-[var(--color-accent)] text-[var(--color-accent-content)] border-[var(--color-accent)] hover:not-disabled:bg-[var(--color-accent-hover)] hover:not-disabled:border-[var(--color-accent-hover)]',
    info: 'bg-[var(--color-info)] text-[var(--color-info-content)] border-[var(--color-info)] hover:not-disabled:bg-[var(--color-info-hover)] hover:not-disabled:border-[var(--color-info-hover)]',
    success:
      'bg-[var(--color-success)] text-[var(--color-success-content)] border-[var(--color-success)] hover:not-disabled:bg-[var(--color-success-hover)] hover:not-disabled:border-[var(--color-success-hover)]',
    warning:
      'bg-[var(--color-warning)] text-[var(--color-warning-content)] border-[var(--color-warning)] hover:not-disabled:bg-[var(--color-warning-hover)] hover:not-disabled:border-[var(--color-warning-hover)]',
    error:
      'bg-[var(--color-error)] text-[var(--color-error-content)] border-[var(--color-error)] hover:not-disabled:bg-[var(--color-error-hover)] hover:not-disabled:border-[var(--color-error-hover)]',
  };

  const btnBase =
    'inline-flex items-center justify-center gap-2 px-4 py-2 text-sm font-medium leading-5 rounded-[var(--radius-field)] cursor-pointer transition-all duration-[var(--animation-btn)] ease-in-out border border-transparent select-none disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2 active:not-disabled:scale-[0.98]';

  const ghostBtnClasses = `${btnBase} bg-transparent border-transparent text-[var(--color-base-content)] hover:not-disabled:bg-[var(--hover-overlay)]`;

  async function handleConfirm() {
    if (!onConfirm || isConfirming) return;

    isConfirming = true;
    try {
      await onConfirm();
    } catch (error) {
      logger.error('Modal onConfirm callback threw an error:', error);
    } finally {
      isConfirming = false;
    }
  }

  function handleBackdropClick(event: MouseEvent) {
    if (closeOnBackdrop && event.target === event.currentTarget) {
      handleClose();
    }
  }

  function handleClose() {
    if (!loading && !isConfirming && onClose) {
      onClose();
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      // Escape that a control inside already used (closing its own list, say)
      // does not also close the dialog, and a dialog under another stays open
      if (event.defaultPrevented || !isTopmost()) return;
      if (closeOnEsc && !loading && !isConfirming) handleClose();
    } else if (event.key === 'Tab') {
      trapFocus(event);
    }
  }

  /**
   * Keeps Tab inside the topmost open Modal. The tabbable controls are read
   * from the DOM on each key press, so controls that appear, disappear, or are
   * enabled or disabled after the dialog opened are accounted for.
   *
   * Native Tab order is left alone while focus is inside the dialog; the trap
   * intervenes only at the boundaries (the last control with Tab, the first
   * with Shift+Tab) and when focus is not on any control of this dialog (on
   * `<body>`, or outside it). Focus inside another `aria-modal` element, such as
   * LoginModal, is left alone. Content portaled outside the dialog element
   * counts as outside, so do not portal focusable content from a Modal body to
   * `document.body`.
   */
  function trapFocus(event: KeyboardEvent) {
    if (event.defaultPrevented || !isTopmost() || !modalElement || !dialogElement) return;

    const active = document.activeElement;

    if (!active || !dialogElement.contains(active)) {
      if (active?.closest(ARIA_MODAL_SELECTOR)) return;
      // Focus is on <body> or on the page behind the dialog: bring it back in
      event.preventDefault();
      const items = getTabbable(modalElement);
      (event.shiftKey ? items.at(-1) : items[0])?.focus();
      if (!dialogElement.contains(document.activeElement)) modalElement.focus();
      return;
    }

    const items = getTabbable(modalElement);
    if (items.length === 0) {
      event.preventDefault();
      modalElement.focus();
      return;
    }

    const exitPoint = tabExitPoint(
      active as HTMLElement,
      getTabbableUncollapsed(modalElement),
      event.shiftKey
    );
    const direction = event.shiftKey
      ? Node.DOCUMENT_POSITION_PRECEDING
      : Node.DOCUMENT_POSITION_FOLLOWING;
    const hasNext = items.some(item => exitPoint.compareDocumentPosition(item) & direction);
    if (!hasNext) {
      event.preventDefault();
      (event.shiftKey ? items.at(-1) : items[0])?.focus();
    }
  }

  function setInitialFocus() {
    if (!modalElement) return;
    (getTabbable(modalElement)[0] ?? modalElement).focus();
  }

  function isFocusInside(): boolean {
    return !!dialogElement && dialogElement.contains(document.activeElement);
  }

  /**
   * Moves focus into the dialog once it can take focus. A dialog that opens
   * with its transition is still hidden when this first runs, so focus() does
   * nothing; it is retried on each animation frame and when the transition
   * ends, until focus is inside the dialog or the bounded wait runs out. A
   * dialog created already open takes focus on the first attempt. Returns a
   * cleanup function that stops any pending retry.
   */
  function scheduleInitialFocus(): () => void {
    let frame: number | undefined;
    let done = false;
    // Read without tracking: the open effect must not depend on the element
    const root = untrack(() => dialogElement);

    const stop = () => {
      done = true;
      clearTimeout(firstAttempt);
      clearTimeout(deadline);
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      root?.removeEventListener('transitionend', attempt);
    };

    function attempt() {
      if (done) return;
      if (!isFocusInside()) setInitialFocus();
      if (isFocusInside()) {
        stop();
        return;
      }
      if (frame === undefined) {
        frame = window.requestAnimationFrame(() => {
          frame = undefined;
          attempt();
        });
      }
    }

    root?.addEventListener('transitionend', attempt);
    const firstAttempt = setTimeout(attempt, 0);
    const deadline = setTimeout(stop, INITIAL_FOCUS_MAX_WAIT_MS);
    return stop;
  }

  function restoreFocus() {
    if (previousActiveElement && 'focus' in previousActiveElement) {
      (previousActiveElement as HTMLElement).focus();
    }
  }

  $effect(() => {
    if (isOpen) {
      previousActiveElement = document.activeElement as HTMLElement;

      const stopInitialFocus = scheduleInitialFocus();

      openModals.push(stackToken);
      document.addEventListener('keydown', handleKeydown);

      return () => {
        stopInitialFocus();
        document.removeEventListener('keydown', handleKeydown);
        const stackIndex = openModals.indexOf(stackToken);
        if (stackIndex !== -1) openModals.splice(stackIndex, 1);
        restoreFocus();
      };
    }
  });
</script>

<div
  bind:this={dialogElement}
  class={cn(
    'fixed inset-0 z-[2000] flex items-center justify-center p-4 bg-black/50 opacity-0 invisible transition-[opacity,visibility] duration-200 ease-out',
    {
      'opacity-100 visible pointer-events-auto': isOpen,
      'pointer-events-none': !isOpen,
    }
  )}
  role="dialog"
  aria-modal="true"
  aria-labelledby={title ? titleId : undefined}
  aria-describedby={describeBody && children ? bodyId : undefined}
  onclick={handleBackdropClick}
  {...rest}
>
  <div
    bind:this={modalElement}
    class={cn(
      // eslint-disable-next-line security/detect-object-injection -- size is typed as ModalSize
      sizeClasses[size],
      { 'scale-100': isOpen },
      className
    )}
    role="document"
    tabindex="-1"
  >
    {#if showCloseButton && type === 'default'}
      <button
        type="button"
        class={cn(
          btnBase,
          'bg-transparent border-transparent text-[var(--color-base-content)] hover:not-disabled:bg-[var(--hover-overlay)]',
          'rounded-[var(--radius-full)] p-2 aspect-square',
          'px-0 py-0 text-[0.8125rem] leading-[1.125rem]',
          'absolute right-2 top-2 size-8'
        )}
        onclick={handleClose}
        disabled={loading || isConfirming}
        aria-label={t('common.aria.closeModal')}
      >
        <X class="size-4" />
      </button>
    {/if}

    {#if header}
      {@render header({ titleId })}
    {:else if title}
      <h3 id={titleId} class="font-bold text-lg mb-4">{title}</h3>
    {/if}

    {#if children}
      <div id={bodyId} class="py-4">
        {@render children()}
      </div>
    {/if}

    {#if footer}
      <div class="flex justify-end gap-2 mt-6">
        {@render footer()}
      </div>
    {:else if type !== 'default'}
      <div class="flex justify-end gap-2 mt-6">
        <button
          type="button"
          class={ghostBtnClasses}
          onclick={handleClose}
          disabled={loading || isConfirming}
        >
          {cancelLabel}
        </button>
        <button
          type="button"
          class={cn(
            btnBase,
            // eslint-disable-next-line security/detect-object-injection -- confirmVariant is typed union
            confirmButtonStyles[confirmVariant]
          )}
          onclick={handleConfirm}
          disabled={loading || isConfirming}
        >
          {#if isConfirming}
            <span
              class="inline-block aspect-square pointer-events-none size-4 border-2 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
            ></span>
          {/if}
          {confirmLabel}
        </button>
      </div>
    {/if}
  </div>
</div>

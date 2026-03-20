<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  // CSS selector for focusable elements used in focus management
  const FOCUSABLE_SELECTOR =
    'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

  type ModalSize =
    | 'sm'
    | 'md'
    | 'lg'
    | 'xl'
    | '2xl'
    | '3xl'
    | '4xl'
    | '5xl'
    | '6xl'
    | '7xl'
    | 'full';
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
    header?: Snippet;
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
  let previousActiveElement: HTMLElement | null = null;
  let focusGeneration = $state(0);

  /**
   * Increment the focus generation counter to force re-evaluation of the
   * cached focusable elements list. Call this after dynamic content changes
   * (e.g., wizard step transitions) that add or remove focusable elements.
   */
  export function refreshFocusTrap() {
    focusGeneration++;
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
      throw error;
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
    if (closeOnEsc && event.key === 'Escape' && !loading && !isConfirming) {
      handleClose();
    } else if (event.key === 'Tab') {
      trapFocus(event);
    }
  }

  // PERFORMANCE OPTIMIZATION: Cache focusable elements with $derived
  // Avoids repeated DOM queries during focus management.
  // Reading focusGeneration ensures the list is refreshed when refreshFocusTrap() is called.
  let focusableElements = $derived.by(() => {
    void focusGeneration; // trigger re-evaluation when incremented
    if (!modalElement) return [];
    return Array.from(modalElement.querySelectorAll(FOCUSABLE_SELECTOR)) as HTMLElement[];
  });

  function trapFocus(event: KeyboardEvent) {
    if (!modalElement) return;

    const elements = focusableElements;
    const firstFocusable = elements[0];
    const lastFocusable = elements[elements.length - 1];

    if (event.shiftKey) {
      if (document.activeElement === firstFocusable) {
        lastFocusable?.focus();
        event.preventDefault();
      }
    } else {
      if (document.activeElement === lastFocusable) {
        firstFocusable?.focus();
        event.preventDefault();
      }
    }
  }

  // PERFORMANCE OPTIMIZATION: Use cached focusable elements
  function setInitialFocus() {
    if (!modalElement) return;

    const elements = focusableElements;

    if (elements.length > 0) {
      elements[0].focus();
    } else {
      modalElement.focus();
    }
  }

  function restoreFocus() {
    if (previousActiveElement && 'focus' in previousActiveElement) {
      (previousActiveElement as HTMLElement).focus();
    }
  }

  $effect(() => {
    if (isOpen) {
      previousActiveElement = document.activeElement as HTMLElement;

      setTimeout(() => setInitialFocus(), 0);

      document.addEventListener('keydown', handleKeydown);

      return () => {
        document.removeEventListener('keydown', handleKeydown);
        restoreFocus();
      };
    }
  });
</script>

<div
  class={cn(
    'fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 opacity-0 invisible transition-[opacity,visibility] duration-200 ease-out',
    { 'opacity-100 visible': isOpen }
  )}
  role="dialog"
  aria-modal="true"
  aria-labelledby={title ? 'modal-title' : undefined}
  aria-describedby={children ? 'modal-body' : undefined}
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
      {@render header()}
    {:else if title}
      <h3 id="modal-title" class="font-bold text-lg mb-4">{title}</h3>
    {/if}

    {#if children}
      <div id="modal-body" class="py-4">
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

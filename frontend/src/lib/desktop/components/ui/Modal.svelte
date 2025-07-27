<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';

  // CSS selector for focusable elements used in focus management
  const FOCUSABLE_SELECTOR =
    'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

  type ModalSize = 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl' | '4xl' | '5xl' | '6xl' | '7xl' | 'full';
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
  let previousActiveElement: Element | null = null;

  const sizeClasses: Record<ModalSize, string> = {
    sm: 'modal-box max-w-sm',
    md: 'modal-box max-w-md',
    lg: 'modal-box max-w-lg',
    xl: 'modal-box max-w-xl',
    '2xl': 'modal-box max-w-2xl',
    '3xl': 'modal-box max-w-3xl',
    '4xl': 'modal-box max-w-4xl',
    '5xl': 'modal-box max-w-5xl',
    '6xl': 'modal-box max-w-6xl',
    '7xl': 'modal-box max-w-7xl',
    full: 'modal-box max-w-full w-full',
  };

  const confirmButtonClasses: Record<typeof confirmVariant, string> = {
    primary: 'btn-primary',
    secondary: 'btn-secondary',
    accent: 'btn-accent',
    info: 'btn-info',
    success: 'btn-success',
    warning: 'btn-warning',
    error: 'btn-error',
  };

  async function handleConfirm() {
    if (!onConfirm || isConfirming) return;

    isConfirming = true;
    try {
      await onConfirm();
    } catch (error) {
      // Log error for debugging
      console.error('Modal onConfirm callback threw an error:', error);
      // Rethrow error so parent component can handle it
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

  function trapFocus(event: KeyboardEvent) {
    if (!modalElement) return;

    const focusableElements = modalElement.querySelectorAll(FOCUSABLE_SELECTOR);
    const firstFocusable = focusableElements[0] as HTMLElement;
    const lastFocusable = focusableElements[focusableElements.length - 1] as HTMLElement;

    if (event.shiftKey) {
      // Shift + Tab - move focus backwards
      if (document.activeElement === firstFocusable) {
        lastFocusable?.focus();
        event.preventDefault();
      }
    } else {
      // Tab - move focus forwards
      if (document.activeElement === lastFocusable) {
        firstFocusable?.focus();
        event.preventDefault();
      }
    }
  }

  function setInitialFocus() {
    if (!modalElement) return;

    // Focus the first focusable element, or the modal itself if no focusable elements
    const focusableElements = modalElement.querySelectorAll(FOCUSABLE_SELECTOR);

    if (focusableElements.length > 0) {
      (focusableElements[0] as HTMLElement).focus();
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
      // Store the currently focused element
      previousActiveElement = document.activeElement;

      // Set focus to modal when opened
      setTimeout(() => setInitialFocus(), 0);

      // Add event listener for keyboard navigation
      document.addEventListener('keydown', handleKeydown);

      return () => {
        document.removeEventListener('keydown', handleKeydown);
        // Restore focus when modal closes
        restoreFocus();
      };
    }
  });
</script>

<div
  class={cn('modal', { 'modal-open': isOpen })}
  role="dialog"
  aria-modal="true"
  aria-labelledby={title ? 'modal-title' : undefined}
  onclick={handleBackdropClick}
  {...rest}
>
  <div bind:this={modalElement} class={cn(sizeClasses[size], className)} tabindex="-1">
    {#if showCloseButton && type === 'default'}
      <button
        type="button"
        class="btn btn-sm btn-circle btn-ghost absolute right-2 top-2"
        onclick={handleClose}
        disabled={loading || isConfirming}
        aria-label={t('common.aria.closeModal')}
      >
        {@html navigationIcons.close}
      </button>
    {/if}

    {#if header}
      {@render header()}
    {:else if title}
      <h3 id="modal-title" class="font-bold text-lg mb-4">{title}</h3>
    {/if}

    {#if children}
      <div class="py-4">
        {@render children()}
      </div>
    {/if}

    {#if footer}
      <div class="modal-action">
        {@render footer()}
      </div>
    {:else if type !== 'default'}
      <div class="modal-action">
        <button
          type="button"
          class="btn btn-ghost"
          onclick={handleClose}
          disabled={loading || isConfirming}
        >
          {cancelLabel}
        </button>
        <button
          type="button"
          class={cn('btn', confirmButtonClasses[confirmVariant])}
          onclick={handleConfirm}
          disabled={loading || isConfirming}
        >
          {#if isConfirming}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {confirmLabel}
        </button>
      </div>
    {/if}
  </div>
</div>

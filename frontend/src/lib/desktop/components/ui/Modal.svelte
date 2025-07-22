<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  type ModalSize = 'sm' | 'md' | 'lg' | 'xl' | 'full';
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
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
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

  const sizeClasses: Record<ModalSize, string> = {
    sm: 'modal-box max-w-sm',
    md: 'modal-box max-w-md',
    lg: 'modal-box max-w-lg',
    xl: 'modal-box max-w-xl',
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
    }
  }

  $effect(() => {
    if (isOpen) {
      document.addEventListener('keydown', handleKeydown);
      return () => {
        document.removeEventListener('keydown', handleKeydown);
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
  <div class={cn(sizeClasses[size], className)}>
    {#if showCloseButton && type === 'default'}
      <button
        type="button"
        class="btn btn-sm btn-circle btn-ghost absolute right-2 top-2"
        onclick={handleClose}
        disabled={loading || isConfirming}
        aria-label="Close modal"
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

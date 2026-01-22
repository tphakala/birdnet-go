<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { onMount } from 'svelte';
  import type { Snippet, Component } from 'svelte';
  import { X, XCircle, TriangleAlert, Info, CircleCheck } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { safeGet } from '$lib/utils/security';

  type ToastType = 'info' | 'success' | 'warning' | 'error';
  type ToastPosition =
    | 'top-left'
    | 'top-center'
    | 'top-right'
    | 'bottom-left'
    | 'bottom-center'
    | 'bottom-right';

  interface ToastAction {
    label: string;
    onClick: () => void;
  }

  interface Props {
    type?: ToastType;
    message: string;
    duration?: number | null; // null means no auto-dismiss
    actions?: ToastAction[];
    position?: ToastPosition;
    showIcon?: boolean;
    onClose?: () => void;
    className?: string;
    children?: Snippet;
  }

  let {
    type = 'info',
    message,
    duration = 5000,
    actions = [],
    position = 'top-right',
    showIcon = true,
    onClose,
    className = '',
    children,
  }: Props = $props();

  let isVisible = $state(true);
  let timeoutId: number | null = null;

  // Native Tailwind classes for toast types - solid backgrounds for readability
  const typeClasses: Record<ToastType, string> = {
    info: 'bg-[var(--color-info)] text-[var(--color-info-content)]',
    success: 'bg-[var(--color-success)] text-[var(--color-success-content)]',
    warning: 'bg-[var(--color-warning)] text-[var(--color-warning-content)]',
    error: 'bg-[var(--color-error)] text-[var(--color-error-content)]',
  };

  // Position classes are intentionally left empty because all positioning styles
  // are applied by the ToastContainer component, which handles the absolute positioning,
  // z-index stacking, and responsive placement of all toast notifications.
  // Individual toasts only handle their own content styling and animations.
  const positionClasses: Record<ToastPosition, string> = {
    'top-left': '',
    'top-center': '',
    'top-right': '',
    'bottom-left': '',
    'bottom-center': '',
    'bottom-right': '',
  };

  // Map toast types to Lucide icon components
  const alertIcons: Record<ToastType, Component> = {
    info: Info,
    success: CircleCheck,
    warning: TriangleAlert,
    error: XCircle,
  };

  function handleClose() {
    isVisible = false;
    if (timeoutId) {
      window.clearTimeout(timeoutId);
      timeoutId = null;
    }
    onClose?.();
  }

  onMount(() => {
    if (duration && duration > 0) {
      timeoutId = window.setTimeout(() => {
        handleClose();
      }, duration);
    }

    return () => {
      if (timeoutId) {
        window.clearTimeout(timeoutId);
      }
    };
  });
</script>

{#if isVisible}
  <div class={cn('w-full max-w-xs', safeGet(positionClasses, position, ''))}>
    <div
      class={cn(
        'flex items-center gap-3 p-4 rounded-lg shadow-lg',
        safeGet(typeClasses, type, typeClasses.info),
        className
      )}
      role="alert"
      aria-live={type === 'error' ? 'assertive' : 'polite'}
    >
      {#if showIcon}
        {@const IconComponent = safeGet(alertIcons, type, Info)}
        <IconComponent class="size-5 shrink-0" aria-hidden="true" />
      {/if}

      <div class="flex-1 min-w-0">
        <div class="text-sm font-medium">{message}</div>
        {#if children}
          {@render children()}
        {/if}
      </div>

      {#if actions.length > 0}
        <div class="flex gap-2 shrink-0">
          {#each actions as action, index (index)}
            <button
              type="button"
              class="inline-flex items-center justify-center px-2 py-1 text-xs font-medium rounded transition-colors bg-white/20 hover:bg-white/30"
              onclick={action.onClick}
            >
              {action.label}
            </button>
          {/each}
        </div>
      {/if}

      <button
        type="button"
        class="inline-flex items-center justify-center p-1 rounded-full shrink-0 transition-colors hover:bg-white/20"
        onclick={handleClose}
        aria-label={t('common.aria.closeNotification')}
      >
        <X class="size-4" />
      </button>
    </div>
  </div>
{/if}

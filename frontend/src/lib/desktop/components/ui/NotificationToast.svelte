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

  const typeClasses: Record<ToastType, string> = {
    info: 'alert-info',
    success: 'alert-success',
    warning: 'alert-warning',
    error: 'alert-error',
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
      class={cn('alert', safeGet(typeClasses, type, 'alert-info'), className)}
      role="alert"
      aria-live={type === 'error' ? 'assertive' : 'polite'}
    >
      {#if showIcon}
        {@const IconComponent = safeGet(alertIcons, type, Info)}
        <IconComponent class="size-6 shrink-0" aria-hidden="true" />
      {/if}

      <div class="flex-1">
        <div>{message}</div>
        {#if children}
          {@render children()}
        {/if}
      </div>

      {#if actions.length > 0}
        <div class="flex gap-2">
          {#each actions as action, index (index)}
            <button type="button" class="btn btn-sm" onclick={action.onClick}>
              {action.label}
            </button>
          {/each}
        </div>
      {/if}

      <button
        type="button"
        class="btn btn-sm btn-circle btn-ghost"
        onclick={handleClose}
        aria-label={t('common.aria.closeNotification')}
      >
        <X class="size-4" />
      </button>
    </div>
  </div>
{/if}

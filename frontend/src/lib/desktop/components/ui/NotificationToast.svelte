<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { navigationIcons, alertIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
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

  // Use centralized alert icons instead of duplicated paths

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
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 shrink-0 stroke-current"
          fill="none"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d={safeGet(alertIcons, type, '')}
          />
        </svg>
      {/if}

      <div class="flex-1">
        <div>{message}</div>
        {#if children}
          {@render children()}
        {/if}
      </div>

      {#if actions.length > 0}
        <div class="flex gap-2">
          {#each actions as action}
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
        {@html navigationIcons.close}
      </button>
    </div>
  </div>
{/if}

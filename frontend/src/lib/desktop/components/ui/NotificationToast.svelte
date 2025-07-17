<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';

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

  const positionClasses: Record<ToastPosition, string> = {
    'top-left': 'toast-start toast-top',
    'top-center': 'toast-center toast-top',
    'top-right': 'toast-end toast-top',
    'bottom-left': 'toast-start toast-bottom',
    'bottom-center': 'toast-center toast-bottom',
    'bottom-right': 'toast-end toast-bottom',
  };

  const iconPaths: Record<ToastType, string> = {
    info: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
    success: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
    warning:
      'M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z',
    error: 'M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z',
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
  <div class={cn('toast', positionClasses[position])}>
    <div
      class={cn('alert', typeClasses[type], className)}
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
            d={iconPaths[type]}
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
        aria-label="Close notification"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-4 w-4"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          aria-hidden="true"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M6 18L18 6M6 6l12 12"
          />
        </svg>
      </button>
    </div>
  </div>
{/if}

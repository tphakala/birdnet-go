<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  type AlertType = 'error' | 'warning' | 'info' | 'success';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    message?: string;
    type?: AlertType;
    dismissible?: boolean;
    onDismiss?: () => void;
    className?: string;
    children?: Snippet;
  }

  let {
    message = '',
    type = 'error',
    dismissible = false,
    onDismiss = () => {},
    className = '',
    children,
    ...rest
  }: Props = $props();

  let isVisible = $state(true);

  const typeClasses: Record<AlertType, string> = {
    error: 'alert-error',
    warning: 'alert-warning',
    info: 'alert-info',
    success: 'alert-success',
  };

  const iconPaths: Record<AlertType, string> = {
    error: 'M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z',
    warning:
      'M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z',
    info: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
    success: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
  };

  function handleDismiss() {
    isVisible = false;
    onDismiss();
  }
</script>

{#if isVisible}
  <div class={cn('alert', typeClasses[type], className)} role="alert" {...rest}>
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-6 w-6 shrink-0 stroke-current"
      fill="none"
      viewBox="0 0 24 24"
    >
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d={iconPaths[type]} />
    </svg>

    <span>
      {#if children}
        {@render children()}
      {:else}
        {message}
      {/if}
    </span>

    {#if dismissible}
      <button
        type="button"
        class="btn btn-sm btn-ghost"
        onclick={handleDismiss}
        aria-label="Dismiss alert"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-4 w-4"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M6 18L18 6M6 6l12 12"
          />
        </svg>
      </button>
    {/if}
  </div>
{/if}

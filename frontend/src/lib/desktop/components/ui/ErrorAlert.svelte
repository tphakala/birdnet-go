<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { alertIcons, type AlertIconType } from '$lib/utils/icons';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  type AlertType = AlertIconType;

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

  // Use centralized icon paths from icons utility
  const iconPaths = alertIcons;

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

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { alertIconsSvg, navigationIcons, type AlertIconType } from '$lib/utils/icons';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';

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

  // Use centralized complete SVG icons
  const iconSvgs = alertIconsSvg;

  function handleDismiss() {
    isVisible = false;
    try {
      onDismiss();
    } catch (error) {
      console.error('Error occurred in ErrorAlert onDismiss callback:', error);
    }
  }
</script>

{#if isVisible}
  <div class={cn('alert', typeClasses[type], className)} role="alert" {...rest}>
    {@html iconSvgs[type]}

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
        aria-label={t('common.aria.dismissAlert')}
      >
        {@html navigationIcons.close}
      </button>
    {/if}
  </div>
{/if}

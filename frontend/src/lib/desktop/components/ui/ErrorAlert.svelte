<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { X, XCircle, TriangleAlert, Info, CircleCheck } from '@lucide/svelte';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { safeGet } from '$lib/utils/security';

  type AlertIconType = 'error' | 'warning' | 'info' | 'success' | 'check';

  const logger = loggers.ui;

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

  // Base alert classes using native Tailwind
  const baseClasses = 'flex items-start gap-3 p-4 rounded-lg';

  // Type-specific classes using native Tailwind with CSS custom properties
  const typeClasses: Record<AlertType, string> = {
    error: 'bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]',
    warning:
      'bg-[color-mix(in_srgb,var(--color-warning)_15%,transparent)] text-[var(--color-warning)]',
    info: 'bg-[color-mix(in_srgb,var(--color-info)_15%,transparent)] text-[var(--color-info)]',
    success:
      'bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)] text-[var(--color-success)]',
    check:
      'bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)] text-[var(--color-success)]',
  };

  function handleDismiss() {
    isVisible = false;
    try {
      onDismiss();
    } catch (error) {
      logger.error('Error occurred in ErrorAlert onDismiss callback:', error);
    }
  }
</script>

{#if isVisible}
  <div
    class={cn(baseClasses, safeGet(typeClasses, type, typeClasses.error), className)}
    role="alert"
    {...rest}
  >
    {#if type === 'error'}
      <XCircle class="size-6 shrink-0" />
    {:else if type === 'warning'}
      <TriangleAlert class="size-6 shrink-0" />
    {:else if type === 'info'}
      <Info class="size-6 shrink-0" />
    {:else if type === 'success' || type === 'check'}
      <CircleCheck class="size-6 shrink-0" />
    {/if}

    <span class="min-w-0">
      {#if children}
        {@render children()}
      {:else}
        {message}
      {/if}
    </span>

    {#if dismissible}
      <button
        type="button"
        class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
        onclick={handleDismiss}
        aria-label={t('common.aria.dismissAlert')}
      >
        <X class="size-4" />
      </button>
    {/if}
  </div>
{/if}

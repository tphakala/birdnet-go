<!--
  StatusBanner Component

  Purpose: Displays a status message banner with appropriate icon and styling
  based on the message type (success, error, info, warning).

  @component
-->
<script lang="ts">
  import { CircleCheck, XCircle, Info } from '@lucide/svelte';

  export type StatusType = 'success' | 'error' | 'info' | 'warning';

  interface Props {
    message: string;
    type?: StatusType;
    class?: string;
  }

  let { message, type = 'info', class: className = '' }: Props = $props();

  const styleMap: Record<StatusType, string> = {
    success: 'bg-[var(--color-success)]/15 text-[var(--color-success)]',
    error: 'bg-[var(--color-error)]/15 text-[var(--color-error)]',
    info: 'bg-[var(--color-info)]/15 text-[var(--color-info)]',
    warning: 'bg-[var(--color-warning)]/15 text-[var(--color-warning)]',
  };

  // eslint-disable-next-line security/detect-object-injection -- type is typed as StatusType
  let typeStyle = $derived(styleMap[type]);
</script>

<div
  class="flex items-center gap-2 rounded-lg p-3 text-sm {typeStyle} {className}"
  role={type === 'error' ? 'alert' : 'status'}
  aria-live={type === 'error' ? 'assertive' : 'polite'}
>
  <div class="shrink-0">
    {#if type === 'success'}
      <CircleCheck class="size-4" />
    {:else if type === 'error'}
      <XCircle class="size-4" />
    {:else}
      <Info class="size-4" />
    {/if}
  </div>
  <span>{message}</span>
</div>

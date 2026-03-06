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
    success: 'bg-success/15 text-success',
    error: 'bg-error/15 text-error',
    info: 'bg-info/15 text-info',
    warning: 'bg-warning/15 text-warning',
  };
</script>

<div
  class="flex items-center gap-2 rounded-lg p-3 text-sm {styleMap[type]} {className}"
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

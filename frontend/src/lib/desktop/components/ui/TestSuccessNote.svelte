<script lang="ts">
  import type { Snippet } from 'svelte';
  import { CircleCheck } from '@lucide/svelte';

  interface Props {
    show: boolean;
    className?: string;
    children?: Snippet;
  }

  let { show, className = '', children }: Props = $props();
</script>

{#if show}
  <div
    class="mt-4 p-4 bg-[var(--color-success)]/10 border border-[var(--color-success)]/20 text-[var(--color-success)] rounded-lg {className}"
  >
    <div class="flex items-start gap-3">
      <div class="w-5 h-5 text-[var(--color-success)] shrink-0 mt-0.5">
        <CircleCheck class="size-5" />
      </div>
      <div class="flex-1">
        {#if children}
          {@render children()}
        {:else}
          <p class="font-medium">Test completed successfully!</p>
          <p class="text-sm mt-1 opacity-90">
            Remember to save your settings to apply the changes permanently.
          </p>
        {/if}
      </div>
    </div>
  </div>
{/if}

<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    show: boolean;
    className?: string;
    children?: Snippet;
  }

  let { show, className = '', children }: Props = $props();
</script>

{#if show}
  <div class="mt-4 p-4 bg-success/10 border border-success/20 text-success rounded-lg {className}">
    <div class="flex items-start gap-3">
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="w-5 h-5 text-success flex-shrink-0 mt-0.5"
        viewBox="0 0 20 20"
        fill="currentColor"
      >
        <path
          fill-rule="evenodd"
          d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
          clip-rule="evenodd"
        />
      </svg>
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

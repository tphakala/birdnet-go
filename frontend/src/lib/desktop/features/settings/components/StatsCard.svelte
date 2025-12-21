<!--
  Stats Card Component

  Purpose: Reusable stats card for displaying metric values with icons.
  Used across settings pages for consistent statistic presentation.

  Props:
  - icon: Lucide icon component to display
  - label: Text label for the statistic
  - value: The value to display (optional if using children snippet)
  - valueClass: Optional additional classes for the value element
  - children: Optional snippet for custom value rendering

  @component
-->
<script lang="ts">
  import type { Snippet, Component } from 'svelte';

  interface Props {
    icon: Component;
    label: string;
    value?: string | number;
    valueClass?: string;
    children?: Snippet;
  }

  let { icon: Icon, label, value, valueClass = '', children }: Props = $props();
</script>

<div class="card bg-base-100 shadow-sm border border-base-300">
  <div class="card-body p-3">
    <div class="flex items-center gap-2 text-[color:var(--color-base-content)] opacity-60">
      <Icon class="size-4" />
      <span class="text-xs font-medium">{label}</span>
    </div>
    {#if children}
      {@render children()}
    {:else if value !== undefined}
      <p class="text-2xl font-bold mt-1 {valueClass}">{value}</p>
    {/if}
  </div>
</div>

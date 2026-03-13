<!--
  DashboardElementWrapper - Wraps dashboard elements in edit mode with controls.
  Shows drag handle, element label, enable/disable toggle, and config gear icon.
  In normal mode, renders children transparently with no visual overhead.

  @component
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { GripVertical, Settings, Eye, EyeOff } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    elementType: string;
    enabled: boolean;
    editMode: boolean;
    onToggle: (_enabled: boolean) => void;
    onConfigure: () => void;
    children: Snippet;
  }

  let { elementType, enabled, editMode, onToggle, onConfigure, children }: Props = $props();

  const elementLabels = new Map<string, string>([
    ['banner', 'Dashboard Banner'],
    ['daily-summary', 'Daily Summary'],
    ['currently-hearing', 'Currently Hearing'],
    ['detections-grid', 'Recent Detections'],
    ['video-embed', 'Video Embed'],
  ]);

  function getLabel(type: string): string {
    return elementLabels.get(type) ?? type;
  }
</script>

{#if editMode}
  <div
    class={cn(
      'relative rounded-xl border-2 border-dashed transition-all',
      enabled
        ? 'border-[var(--color-primary)]/40 bg-[var(--color-base-100)]'
        : 'border-[var(--color-base-300)] bg-[var(--color-base-200)]/50 opacity-60'
    )}
  >
    <!-- Edit mode toolbar -->
    <div
      class="flex items-center gap-2 rounded-t-xl border-b border-[var(--color-base-200)] bg-[var(--color-base-200)]/50 px-3 py-2"
    >
      <!-- Drag handle -->
      <GripVertical class="size-5 shrink-0 cursor-grab text-[var(--color-base-content)]/40" />

      <!-- Element label -->
      <span class="flex-1 text-sm font-medium text-[var(--color-base-content)]/70">
        {getLabel(elementType)}
      </span>

      <!-- Enable/disable toggle -->
      <button
        onclick={() => onToggle(!enabled)}
        class="rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5"
        aria-label={enabled ? 'Disable element' : 'Enable element'}
      >
        {#if enabled}
          <Eye class="size-4 text-[var(--color-success)]" />
        {:else}
          <EyeOff class="size-4 text-[var(--color-base-content)]/40" />
        {/if}
      </button>

      <!-- Configure button -->
      <button
        onclick={onConfigure}
        class="rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5"
        aria-label="Configure element"
      >
        <Settings class="size-4 text-[var(--color-base-content)]/60" />
      </button>
    </div>

    <!-- Element content (dimmed if disabled) -->
    <div class={cn('p-2', !enabled && 'pointer-events-none')}>
      {#if enabled}
        {@render children()}
      {:else}
        <div class="py-8 text-center text-sm text-[var(--color-base-content)]/40">
          {getLabel(elementType)} — disabled
        </div>
      {/if}
    </div>
  </div>
{:else}
  {@render children()}
{/if}

<!--
  DashboardElementWrapper - Wraps dashboard elements in edit mode with controls.
  Shows drag handle, element label, enable/disable toggle, and config gear icon.
  In normal mode, renders children transparently with no visual overhead.

  @component
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { GripVertical, Settings, EyeOff, Eye, Trash2 } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn.js';
  import { t } from '$lib/i18n';
  import { getElementLabel } from '$lib/desktop/features/dashboard/utils/elementLabels';

  interface Props {
    elementType: string;
    enabled: boolean;
    editMode: boolean;
    onHide: () => void;
    onUnhide: () => void;
    onDelete: () => void;
    onConfigure: () => void;
    children: Snippet;
  }

  let { elementType, enabled, editMode, onHide, onUnhide, onDelete, onConfigure, children }: Props =
    $props();
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
        {getElementLabel(elementType)}
      </span>

      <!-- Hide/Unhide button -->
      <button
        onclick={() => (enabled ? onHide() : onUnhide())}
        class="rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5"
        aria-label={enabled
          ? t('dashboard.editMode.hideElement')
          : t('dashboard.editMode.unhideElement')}
      >
        {#if enabled}
          <EyeOff class="size-4 text-[var(--color-base-content)]/60" />
        {:else}
          <Eye class="size-4 text-[var(--color-success)]" />
        {/if}
      </button>

      <!-- Delete button -->
      <button
        onclick={onDelete}
        class="rounded-md p-1.5 transition-colors hover:bg-[var(--color-error)]/10"
        aria-label={t('dashboard.editMode.deleteElement')}
      >
        <Trash2 class="size-4 text-[var(--color-error)]/60" />
      </button>

      <!-- Configure button -->
      <button
        onclick={onConfigure}
        class="rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5"
        aria-label={t('dashboard.editMode.configureElement')}
      >
        <Settings class="size-4 text-[var(--color-base-content)]/60" />
      </button>
    </div>

    <!-- Element content -->
    <div class={cn('p-2', !enabled && 'pointer-events-none')}>
      {#if enabled}
        {@render children()}
      {:else}
        <div
          class="flex items-center justify-center py-4 text-sm text-[var(--color-base-content)]/40"
        >
          <EyeOff class="mr-2 size-4" />
          {getElementLabel(elementType)} — {t('dashboard.editMode.disabled')}
        </div>
      {/if}
    </div>
  </div>
{:else}
  {@render children()}
{/if}

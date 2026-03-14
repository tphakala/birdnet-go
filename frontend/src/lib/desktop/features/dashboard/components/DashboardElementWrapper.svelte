<!--
  DashboardElementWrapper - Wraps dashboard elements in edit mode with controls.
  Shows drag handle, element label, enable/disable toggle, width toggle, and cogwheel settings.
  Settings content is passed in via the settingsContent snippet prop and rendered in a dropdown.
  In normal mode, renders children transparently with no visual overhead.

  @component
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { DashboardElement } from '$lib/stores/settings';
  import { GripVertical, EyeOff, Eye, Trash2, Columns2, Square, Settings } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn.js';
  import { t } from '$lib/i18n';
  import { getElementLabel } from '$lib/desktop/features/dashboard/utils/elementLabels';

  // Element types that always require full width
  const FULL_WIDTH_ONLY: string[] = ['daily-summary'];

  // Element types that support half width
  const SUPPORTS_HALF: string[] = ['banner', 'video-embed', 'currently-hearing', 'detections-grid'];

  interface Props {
    element: DashboardElement;
    editMode: boolean;
    onHide: () => void;
    onUnhide: () => void;
    onDelete: () => void;
    onUpdate: (_element: DashboardElement) => void;
    children: Snippet;
    settingsContent?: Snippet;
  }

  let {
    element,
    editMode,
    onHide,
    onUnhide,
    onDelete,
    onUpdate,
    children,
    settingsContent,
  }: Props = $props();

  let canHalf = $derived(SUPPORTS_HALF.includes(element.type));
  let isFullWidthOnly = $derived(FULL_WIDTH_ONLY.includes(element.type));
  let currentWidth = $derived(element.width ?? 'full');

  let settingsOpen = $state(false);

  function toggleWidth() {
    onUpdate({ ...element, width: currentWidth === 'full' ? 'half' : 'full' });
  }

  function handleSettingsClickOutside(event: MouseEvent) {
    if (settingsOpen) {
      const target = event.target as HTMLElement;
      if (!target.closest('.settings-dropdown-container')) {
        settingsOpen = false;
      }
    }
  }
</script>

<svelte:window onclick={handleSettingsClickOutside} />

{#if editMode}
  <div
    class={cn(
      'relative rounded-xl border-2 border-dashed transition-all',
      element.enabled
        ? 'border-[var(--color-primary)]/40 bg-[var(--color-base-100)]'
        : 'border-[var(--color-base-300)] bg-[var(--color-base-200)]/50 opacity-60',
      settingsOpen && 'z-50'
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
        {getElementLabel(element.type)}
      </span>

      <!-- Width toggle (only for elements that support half width) -->
      {#if canHalf}
        <button
          onclick={toggleWidth}
          class="flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-black/5 dark:hover:bg-white/5"
          aria-label={currentWidth === 'full'
            ? t('dashboard.editMode.widthHalf')
            : t('dashboard.editMode.widthFull')}
          title={currentWidth === 'full'
            ? t('dashboard.editMode.widthHalf')
            : t('dashboard.editMode.widthFull')}
        >
          {#if currentWidth === 'half'}
            <Square class="size-3.5 text-[var(--color-base-content)]/60" />
            <span class="text-[var(--color-base-content)]/60"
              >{t('dashboard.editMode.widthFull')}</span
            >
          {:else}
            <Columns2 class="size-3.5 text-[var(--color-base-content)]/60" />
            <span class="text-[var(--color-base-content)]/60"
              >{t('dashboard.editMode.widthHalf')}</span
            >
          {/if}
        </button>
      {:else if isFullWidthOnly}
        <span class="text-xs text-[var(--color-base-content)]/40">
          {t('dashboard.editMode.fullWidthOnly')}
        </span>
      {/if}

      <!-- Hide/Unhide button -->
      <button
        onclick={() => (element.enabled ? onHide() : onUnhide())}
        class="rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5"
        aria-label={element.enabled
          ? t('dashboard.editMode.hideElement')
          : t('dashboard.editMode.unhideElement')}
      >
        {#if element.enabled}
          <EyeOff class="size-4 text-[var(--color-base-content)]/60" />
        {:else}
          <Eye class="size-4 text-[var(--color-success)]" />
        {/if}
      </button>

      <!-- Cogwheel settings button (only when settingsContent is provided) -->
      {#if settingsContent}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="settings-dropdown-container relative">
          <button
            onclick={e => {
              e.stopPropagation();
              settingsOpen = !settingsOpen;
            }}
            class={cn(
              'rounded-md p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/5',
              settingsOpen && 'bg-black/5 dark:bg-white/5'
            )}
            aria-label={t('dashboard.editMode.settings')}
          >
            <Settings class="size-4 text-[var(--color-base-content)]/60" />
          </button>

          {#if settingsOpen}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="absolute right-0 top-full z-50 mt-2 min-w-64 rounded-lg border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-4 shadow-xl"
              onmousedown={e => e.stopPropagation()}
              onclick={e => e.stopPropagation()}
              onkeydown={e => e.stopPropagation()}
            >
              {@render settingsContent()}
            </div>
          {/if}
        </div>
      {/if}

      <!-- Delete button -->
      <button
        onclick={onDelete}
        class="rounded-md p-1.5 transition-colors hover:bg-[var(--color-error)]/10"
        aria-label={t('dashboard.editMode.deleteElement')}
      >
        <Trash2 class="size-4 text-[var(--color-error)]/60" />
      </button>
    </div>

    <!-- Element content -->
    <div class={cn('p-2', !element.enabled && 'pointer-events-none')}>
      {#if element.enabled}
        {@render children()}
      {:else}
        <div
          class="flex items-center justify-center py-4 text-sm text-[var(--color-base-content)]/40"
        >
          <EyeOff class="mr-2 size-4" />
          {getElementLabel(element.type)} — {t('dashboard.editMode.disabled')}
        </div>
      {/if}
    </div>
  </div>
{:else}
  {@render children()}
{/if}

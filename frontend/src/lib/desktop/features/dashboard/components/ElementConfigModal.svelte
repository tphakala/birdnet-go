<!--
  Modal dialog for configuring individual dashboard elements.
  Renders the appropriate config form based on element type.
  @component
-->
<script lang="ts">
  import { X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { DashboardElement } from '$lib/stores/settings';
  import { getElementLabel } from '$lib/desktop/features/dashboard/utils/elementLabels';
  import BannerConfigForm from './BannerConfigForm.svelte';
  import DailySummaryConfigForm from './DailySummaryConfigForm.svelte';
  import DetectionsGridConfigForm from './DetectionsGridConfigForm.svelte';
  import VideoEmbedConfigForm from './VideoEmbedConfigForm.svelte';

  interface Props {
    element: DashboardElement;
    open: boolean;
    onSave: (_element: DashboardElement) => void;
    onClose: () => void;
  }

  let { element, open, onSave, onClose }: Props = $props();

  // Local mutable copy for editing
  let editElement = $state<DashboardElement>({ type: 'daily-summary', enabled: true });

  // Reset local copy when the source element changes
  $effect(() => {
    // Read element prop to create dependency, deep clone plain data
    editElement = JSON.parse(JSON.stringify(element)) as DashboardElement;
  });

  function handleSave() {
    onSave(editElement);
    onClose();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') onClose();
  }
</script>

{#if open}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    onclick={onClose}
    onkeydown={handleKeydown}
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label={t('dashboard.editMode.configureTitle', { element: getElementLabel(element.type) })}
  >
    <!-- Modal content -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="mx-4 w-full max-w-lg rounded-2xl bg-[var(--color-base-100)] p-6 shadow-2xl"
      onclick={e => e.stopPropagation()}
      onkeydown={e => e.stopPropagation()}
    >
      <!-- Header -->
      <div class="mb-4 flex items-center justify-between">
        <h3 class="text-lg font-semibold text-[var(--color-base-content)]">
          {t('dashboard.editMode.configureTitle', { element: getElementLabel(element.type) })}
        </h3>
        <button
          onclick={onClose}
          class="rounded-full p-1.5 transition-colors hover:bg-black/5 dark:hover:bg-white/10"
          aria-label={t('dashboard.editMode.closeModal')}
        >
          <X class="size-5" />
        </button>
      </div>

      <!-- Config form based on element type -->
      <div class="mb-6">
        {#if editElement.type === 'banner'}
          <BannerConfigForm
            config={editElement.banner ?? {
              showImage: false,
              imagePath: '',
              title: '',
              description: '',
              showLocationMap: false,
              showWeather: false,
            }}
            onUpdate={config => {
              editElement = { ...editElement, banner: config };
            }}
          />
        {:else if editElement.type === 'daily-summary'}
          <DailySummaryConfigForm
            config={editElement.summary ?? { summaryLimit: 30 }}
            onUpdate={config => {
              editElement = { ...editElement, summary: config };
            }}
          />
        {:else if editElement.type === 'detections-grid'}
          <DetectionsGridConfigForm
            config={editElement.grid ?? {}}
            onUpdate={config => {
              editElement = { ...editElement, grid: config };
            }}
          />
        {:else if editElement.type === 'video-embed'}
          <VideoEmbedConfigForm
            config={editElement.video ?? { url: '', title: '' }}
            onUpdate={config => {
              editElement = { ...editElement, video: config };
            }}
          />
        {:else if editElement.type === 'currently-hearing'}
          <p class="text-sm text-[var(--color-base-content)]/60">
            {t('dashboard.editMode.currentlyHearingNote')}
          </p>
        {:else if editElement.type === 'search'}
          <p class="text-sm text-[var(--color-base-content)]/60">
            {t('dashboard.editMode.searchNote')}
          </p>
        {:else}
          <p class="text-sm text-[var(--color-base-content)]/60">
            {t('dashboard.editMode.notAvailable')}
          </p>
        {/if}
      </div>

      <!-- Footer -->
      <div class="flex justify-end gap-3">
        <button
          onclick={onClose}
          class="rounded-lg border border-[var(--color-base-content)]/30 bg-transparent px-4 py-2 text-sm font-medium transition-colors hover:bg-black/5 dark:hover:bg-white/10"
        >
          {t('dashboard.editMode.cancel')}
        </button>
        <button
          onclick={handleSave}
          class="rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] transition-colors hover:opacity-90"
        >
          {t('dashboard.editMode.save')}
        </button>
      </div>
    </div>
  </div>
{/if}

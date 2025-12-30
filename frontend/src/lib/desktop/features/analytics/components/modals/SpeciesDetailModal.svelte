<script lang="ts">
  import { t } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';
  import { X } from '@lucide/svelte';

  interface SpeciesData {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
    max_confidence: number;
    first_heard: string;
    last_heard: string;
    thumbnail_url?: string;
  }

  interface Props {
    species: SpeciesData | null;
    isOpen: boolean;
    onClose?: () => void;
  }

  let { species, isOpen, onClose }: Props = $props();

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  function formatDate(dateString: string): string {
    if (!dateString) return '';
    const date = parseLocalDateString(dateString);
    if (!date) return '';
    return date.toLocaleDateString();
  }

  function handleClose() {
    if (onClose) onClose();
  }

  function handleOverlayClick(e: MouseEvent) {
    // Close when clicking outside the dialog content
    e.stopPropagation();
    handleClose();
  }

  function stopPropagation(e: MouseEvent) {
    e.stopPropagation();
  }

  function stopKeyPropagation(e: KeyboardEvent) {
    e.stopPropagation();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      handleClose();
    }
  }

  $effect(() => {
    if (isOpen) {
      document.addEventListener('keydown', handleKeydown);
      return () => {
        document.removeEventListener('keydown', handleKeydown);
      };
    }
  });
</script>

{#if isOpen && species}
  <!-- Accessible modal overlay -->
  <div
    class="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-base-200/70"
    role="dialog"
    aria-modal="true"
    aria-label={species.common_name}
    tabindex="-1"
    onclick={handleOverlayClick}
    onkeydown={handleKeydown}
  >
    <!-- Bottom sheet on mobile, centered dialog on larger screens -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="bg-base-100 w-full sm:max-w-md sm:rounded-xl sm:shadow-xl sm:my-8 rounded-t-2xl"
      style:max-height="80vh"
      onclick={stopPropagation}
      onkeydown={stopKeyPropagation}
    >
      <!-- Header -->
      <div class="flex items-center justify-between p-4 border-b border-base-300">
        <div class="min-w-0">
          <h2 class="font-bold text-lg truncate">{species.common_name}</h2>
          <p class="text-sm text-base-content opacity-70 italic truncate">
            {species.scientific_name}
          </p>
        </div>
        <button
          class="btn btn-ghost btn-sm"
          aria-label={t('common.aria.closeModal')}
          onclick={handleClose}
        >
          <X class="size-5" />
        </button>
      </div>

      <!-- Content -->
      <div class="p-4 space-y-3 overflow-y-auto">
        {#if species.thumbnail_url}
          <div class="w-full h-40 rounded-xl overflow-hidden bg-base-300">
            <img
              src={species.thumbnail_url}
              alt={species.common_name}
              class="w-full h-full object-cover"
            />
          </div>
        {/if}

        <div class="grid grid-cols-2 gap-3 text-sm">
          <div class="flex justify-between bg-base-200 rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.card.detections')}</span>
            <span class="font-semibold">{species.count}</span>
          </div>
          <div class="flex justify-between bg-base-200 rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.card.confidence')}</span>
            <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
          </div>
          {#if species.first_heard}
            <div class="flex justify-between bg-base-200 rounded px-3 py-2">
              <span class="opacity-70">{t('analytics.species.headers.firstDetected')}</span>
              <span class="font-semibold">{formatDate(species.first_heard)}</span>
            </div>
          {/if}
          {#if species.last_heard}
            <div class="flex justify-between bg-base-200 rounded px-3 py-2">
              <span class="opacity-70">{t('analytics.species.headers.lastDetected')}</span>
              <span class="font-semibold">{formatDate(species.last_heard)}</span>
            </div>
          {/if}
        </div>
      </div>

      <!-- Footer -->
      <div class="p-4 border-t border-base-300">
        <button class="btn btn-primary w-full" onclick={handleClose}>{t('common.close')}</button>
      </div>
    </div>
  </div>
{/if}

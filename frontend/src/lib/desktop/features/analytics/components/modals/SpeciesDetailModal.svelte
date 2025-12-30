<script lang="ts">
  import { t } from '$lib/i18n';
  import { parseLocalDateString } from '$lib/utils/date';

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
</script>

{#if isOpen && species}
  <!-- Accessible modal overlay -->
  <div
    class="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-base-200/70"
    role="dialog"
    aria-modal="true"
    aria-label={species.common_name}
    onclick={handleOverlayClick}
  >
    <!-- Bottom sheet on mobile, centered dialog on larger screens -->
    <div
      class="bg-base-100 w-full sm:max-w-md sm:rounded-xl sm:shadow-xl sm:my-8 rounded-t-2xl"
      style:max-height="80vh"
      onclick={stopPropagation}
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
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
              clip-rule="evenodd"
            />
          </svg>
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

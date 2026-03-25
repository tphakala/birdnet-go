<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
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
</script>

{#if species}
  <Modal
    isOpen={isOpen && species !== null}
    title={species.common_name}
    size="md"
    type="default"
    onClose={handleClose}
    className="sm:modal-middle"
  >
    {#snippet header()}
      <div class="flex items-center justify-between">
        <div class="min-w-0">
          <h3 id="modal-title" class="font-bold text-lg truncate">{species.common_name}</h3>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 italic truncate">
            {species.scientific_name}
          </p>
        </div>
      </div>
    {/snippet}

    {#snippet children()}
      {#if species.thumbnail_url}
        <div class="w-full aspect-[4/3] rounded-xl overflow-hidden bg-[var(--color-base-300)]">
          <img
            src={species.thumbnail_url}
            alt={species.common_name}
            class="w-full h-full object-cover"
          />
        </div>
      {/if}

      <div class="grid grid-cols-2 gap-3 text-sm mt-3">
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.detections')}</span>
          <span class="font-semibold">{species.count}</span>
        </div>
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.confidence')}</span>
          <span class="font-semibold">{formatPercentage(species.avg_confidence)}</span>
        </div>
        {#if species.first_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.firstDetected')}</span>
            <span class="font-semibold">{formatDate(species.first_heard)}</span>
          </div>
        {/if}
        {#if species.last_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.lastDetected')}</span>
            <span class="font-semibold">{formatDate(species.last_heard)}</span>
          </div>
        {/if}
      </div>
    {/snippet}

    {#snippet footer()}
      <button
        class="px-4 py-2 rounded-lg font-medium transition-colors w-full
               bg-[var(--color-primary)] text-[var(--color-primary-content)]
               hover:bg-[var(--color-primary)]/90"
        onclick={handleClose}
      >
        {t('common.close')}
      </button>
    {/snippet}
  </Modal>
{/if}

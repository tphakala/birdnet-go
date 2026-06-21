<script lang="ts">
  import { untrack } from 'svelte';
  import { BookOpen } from '@lucide/svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { formatDate } from '$lib/utils/formatters';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { dashboardSettings } from '$lib/stores/settings';
  import SpeciesComparison from '$lib/desktop/components/ui/SpeciesComparison.svelte';
  import SpeciesNotes from '$lib/desktop/components/ui/SpeciesNotes.svelte';

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

  // Cache species data so content persists during the Modal close animation.
  // Updated when a new species is provided, retained when species becomes null
  // while isOpen transitions to false.
  let cachedSpecies = $state<SpeciesData | null>(null);

  // Clear stale cache when the modal opens so previous species data doesn't flash.
  // The cache is only useful during the close transition (species becomes null while
  // isOpen transitions to false), not during open.
  let prevIsOpen = $state(false);
  // Species guide panel state (gated on settings; reset each time the modal opens).
  let guidePanelOpen = $state(true);
  let guideEnabled = $derived($dashboardSettings?.speciesGuide?.enabled ?? false);
  let showSimilarSpecies = $derived($dashboardSettings?.speciesGuide?.showSimilarSpecies ?? true);
  let showNotes = $derived($dashboardSettings?.speciesGuide?.showNotes ?? true);
  $effect(() => {
    if (isOpen && !untrack(() => prevIsOpen)) {
      cachedSpecies = null;
      guidePanelOpen = true;
    }
    prevIsOpen = isOpen;
  });

  $effect(() => {
    if (species) {
      cachedSpecies = species;
    }
  });

  // Use cached data for rendering, fall back to current prop
  let displaySpecies = $derived(species ?? cachedSpecies);
  let displayName = $derived(
    localizeSpeciesName(displaySpecies?.scientific_name, displaySpecies?.common_name)
  );

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  function handleClose() {
    if (onClose) onClose();
  }
</script>

<Modal
  isOpen={isOpen && displaySpecies !== null}
  title={displayName}
  size="md"
  type="default"
  onClose={handleClose}
  className="sm:modal-middle"
  aria-labelledby="species-detail-modal-title"
>
  {#snippet header()}
    {#if displaySpecies}
      <div class="flex items-center justify-between">
        <div class="min-w-0">
          <h3 id="species-detail-modal-title" class="font-bold text-lg truncate">
            {displayName}
          </h3>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 italic truncate">
            {displaySpecies.scientific_name}
          </p>
        </div>
      </div>
    {/if}
  {/snippet}

  {#snippet children()}
    {#if displaySpecies}
      {#if displaySpecies.thumbnail_url}
        <div class="w-full aspect-[4/3] rounded-xl overflow-hidden bg-[var(--color-base-300)]">
          <img
            src={displaySpecies.thumbnail_url}
            alt={displayName}
            class="w-full h-full object-cover"
          />
        </div>
      {/if}

      <div class="grid grid-cols-2 gap-3 text-sm mt-3">
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.detections')}</span>
          <span class="font-semibold">{displaySpecies.count}</span>
        </div>
        <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
          <span class="opacity-70">{t('analytics.species.card.confidence')}</span>
          <span class="font-semibold">{formatPercentage(displaySpecies.avg_confidence)}</span>
        </div>
        {#if displaySpecies.first_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.firstDetected')}</span>
            <span class="font-semibold">{formatDate(displaySpecies.first_heard)}</span>
          </div>
        {/if}
        {#if displaySpecies.last_heard}
          <div class="flex justify-between bg-[var(--color-base-200)] rounded px-3 py-2">
            <span class="opacity-70">{t('analytics.species.headers.lastDetected')}</span>
            <span class="font-semibold">{formatDate(displaySpecies.last_heard)}</span>
          </div>
        {/if}
      </div>

      {#if guideEnabled && (showNotes || showSimilarSpecies)}
        <div class="mt-4 space-y-4 border-t border-[var(--color-base-300)] pt-4">
          <!-- Key on the species so the guide + notes remount (and refetch) when
               the modal is reused for a different species without closing. -->
          {#key displaySpecies.scientific_name}
            {#if showSimilarSpecies}
              {#if guidePanelOpen}
                <SpeciesComparison
                  scientificName={displaySpecies.scientific_name}
                  commonName={displayName}
                  heading={t('analytics.species.guide.title')}
                  onclose={() => (guidePanelOpen = false)}
                />
              {:else}
                <!-- Reopen affordance so closing the comparison is never a dead end. -->
                <button
                  type="button"
                  class="btn btn-ghost btn-sm gap-2"
                  onclick={() => (guidePanelOpen = true)}
                >
                  <BookOpen class="h-4 w-4" />
                  {t('analytics.species.similar.show')}
                </button>
              {/if}
            {/if}
            {#if showNotes}
              <SpeciesNotes scientificName={displaySpecies.scientific_name} />
            {/if}
          {/key}
        </div>
      {/if}
    {/if}
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

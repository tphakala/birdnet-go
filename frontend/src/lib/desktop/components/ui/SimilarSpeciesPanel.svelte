<script lang="ts">
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import {
    extractCanonicalSections,
    type CanonicalSectionId,
    type CanonicalSections,
    type SimilarSpeciesEntry,
    type SpeciesGuideData,
  } from '$lib/types/species';

  const logger = loggers.ui;

  // 404: the selected species has no guide content. Shown as a soft message,
  // not an error alert (mirrors SpeciesComparison's handling).
  const HTTP_NOT_FOUND = 404;

  interface Props {
    /** Common (or scientific) name of the focal species, for the "vs …" header. */
    mainName: string;
    similar: SimilarSpeciesEntry[];
    className?: string;
  }

  let { mainName, similar, className = '' }: Props = $props();

  // Canonical rows shown in the diff card, in display order. Behaviour is
  // extracted but intentionally not surfaced here to keep the card focused.
  const SECTION_ROWS: { id: CanonicalSectionId; labelKey: string }[] = [
    { id: 'appearance', labelKey: 'analytics.species.similar.sections.appearance' },
    { id: 'voice', labelKey: 'analytics.species.similar.sections.voice' },
    { id: 'habitat', labelKey: 'analytics.species.similar.sections.habitat' },
  ];

  type LoadStatus = 'loading' | 'ready' | 'notfound' | 'error';

  let selected = $state<string | null>(null);
  // Per-species fetch cache + status, keyed by scientific name, so re-selecting
  // a species is instant and we don't re-hit the rate-limited guide endpoint.
  let guides = $state<Record<string, CanonicalSections>>({});
  let status = $state<Record<string, LoadStatus>>({});

  let selectedEntry = $derived(similar.find(e => e.scientific_name === selected) ?? null);
  let selectedSections = $derived(selected ? (guides[selected] ?? null) : null);
  let selectedStatus = $derived<LoadStatus | undefined>(selected ? status[selected] : undefined);
  // Non-empty canonical rows for the selected species.
  let visibleRows = $derived(
    selectedSections ? SECTION_ROWS.filter(row => selectedSections[row.id].trim() !== '') : []
  );

  async function fetchGuide(name: string): Promise<void> {
    status[name] = 'loading';
    try {
      const enc = encodeURIComponent(name);
      const g = await api.get<SpeciesGuideData>(`/api/v2/species/${enc}/guide`);
      guides[name] = extractCanonicalSections(g.description);
      status[name] = 'ready';
    } catch (e) {
      if (e instanceof ApiError && e.status === HTTP_NOT_FOUND) {
        status[name] = 'notfound';
      } else {
        status[name] = 'error';
        logger.error('Failed to load similar-species guide', e, {
          component: 'SimilarSpeciesPanel',
        });
      }
    }
  }

  function select(name: string): void {
    selected = name;
    // Refetch on a prior error (retry); skip when cached or already loading.
    if (guides[name] || status[name] === 'loading') return;
    void fetchGuide(name);
  }

  // Auto-select the first species that has a guide so the card is never empty.
  // Also re-run when the current selection is no longer in the list (e.g. the
  // focal species changed): otherwise a stale `selected` would suppress
  // auto-select and could surface the previous species' cached sections.
  $effect(() => {
    if (selected === null || !similar.some(e => e.scientific_name === selected)) {
      const first = similar.find(e => e.has_guide);
      if (first) {
        select(first.scientific_name);
      } else if (selected !== null) {
        selected = null;
      }
    }
  });
</script>

<div class={`similar-species-panel ${className}`} data-testid="similar-species-panel">
  {#if similar.length === 0}
    <p class="text-sm text-base-content/70">{t('analytics.species.similar.empty')}</p>
  {:else}
    <div class="grid grid-cols-[minmax(7rem,9rem)_1fr] gap-3">
      <!-- Picker rail -->
      <ul
        class="flex flex-col gap-1 border-r border-base-300 pr-2"
        aria-label={t('analytics.species.similar.title')}
      >
        {#each similar as entry (entry.scientific_name)}
          <li>
            <button
              type="button"
              class="w-full rounded-md px-2 py-1.5 text-left text-sm transition-colors
                {entry.scientific_name === selected
                ? 'bg-primary/10 text-primary font-medium'
                : 'hover:bg-base-200'}
                {entry.has_guide ? '' : 'cursor-not-allowed opacity-60'}"
              disabled={!entry.has_guide}
              title={entry.has_guide ? undefined : t('analytics.species.similar.noGuideAvailable')}
              aria-pressed={entry.scientific_name === selected}
              onclick={() => select(entry.scientific_name)}
            >
              <span class="block truncate">{entry.common_name || entry.scientific_name}</span>
              {#if !entry.has_guide}
                <span class="block text-xs text-base-content/50">
                  {t('analytics.species.similar.noGuideAvailable')}
                </span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>

      <!-- Diff card -->
      <div class="min-w-0">
        {#if !selectedEntry}
          <p class="text-sm text-base-content/70">
            {t('analytics.species.similar.selectPrompt', { species: mainName })}
          </p>
        {:else}
          <div class="mb-2">
            <p class="font-medium leading-tight">
              {selectedEntry.common_name || selectedEntry.scientific_name}
            </p>
            <p class="text-xs text-base-content/60">
              {t('analytics.species.similar.versus', { species: mainName })}
            </p>
          </div>

          {#if selectedStatus === 'loading'}
            <div
              role="status"
              aria-live="polite"
              class="flex items-center gap-2 text-sm text-base-content/70"
            >
              <span
                class="animate-spin h-4 w-4 border-2 border-primary border-t-transparent rounded-full"
                aria-hidden="true"
              ></span>
              <span>{t('analytics.species.similar.cardLoading')}</span>
            </div>
          {:else if selectedStatus === 'notfound'}
            <p class="text-sm text-base-content/70">{t('analytics.species.similar.cardNoGuide')}</p>
          {:else if selectedStatus === 'error'}
            <p role="alert" class="text-sm text-error">
              {t('analytics.species.similar.cardError')}
            </p>
          {:else if visibleRows.length === 0}
            <p class="text-sm text-base-content/70">
              {t('analytics.species.similar.cardNoSections')}
            </p>
          {:else}
            <dl class="flex flex-col gap-3">
              {#each visibleRows as row (row.id)}
                <div>
                  <dt class="text-xs uppercase tracking-wide text-base-content/50">
                    {t(row.labelKey)}
                  </dt>
                  <dd class="mt-0.5 text-sm whitespace-pre-line">
                    {selectedSections?.[row.id]}
                  </dd>
                </div>
              {/each}
            </dl>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</div>

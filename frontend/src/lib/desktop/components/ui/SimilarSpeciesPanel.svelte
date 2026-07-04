<script lang="ts">
  import { untrack } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import { ExternalLink } from '@lucide/svelte';
  import { t, getLocale } from '$lib/i18n';
  import ExternalLinkBadge from '$lib/desktop/components/ui/ExternalLinkBadge.svelte';
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

  // Canonical rows shown in the diff card, in display order.
  const SECTION_ROWS: { id: CanonicalSectionId; labelKey: string }[] = [
    { id: 'appearance', labelKey: 'analytics.species.similar.sections.appearance' },
    { id: 'voice', labelKey: 'analytics.species.similar.sections.voice' },
    { id: 'habitat', labelKey: 'analytics.species.similar.sections.habitat' },
    { id: 'behaviour', labelKey: 'analytics.species.similar.sections.behaviour' },
  ];

  type LoadStatus = 'loading' | 'ready' | 'notfound' | 'error';

  let selected = $state<string | null>(null);
  // Per-species fetch cache + status, keyed by scientific name, so re-selecting
  // a species is instant and we don't re-hit the rate-limited guide endpoint.
  // SvelteMap keeps keyed access reactive without indexing a plain object.
  const guides = new SvelteMap<string, CanonicalSections>();
  const status = new SvelteMap<string, LoadStatus>();

  let selectedEntry = $derived(similar.find(e => e.scientific_name === selected) ?? null);
  let selectedSections = $derived(selected ? (guides.get(selected) ?? null) : null);
  let selectedStatus = $derived<LoadStatus | undefined>(
    selected ? status.get(selected) : undefined
  );
  // Non-empty canonical rows for the selected species.
  let visibleRows = $derived(
    selectedSections ? SECTION_ROWS.filter(row => selectedSections[row.id].trim() !== '') : []
  );

  async function fetchGuide(name: string): Promise<void> {
    status.set(name, 'loading');
    try {
      const enc = encodeURIComponent(name);
      const g = await api.get<SpeciesGuideData>(
        `/api/v2/species/${enc}/guide?locale=${encodeURIComponent(getLocale())}`
      );
      // The focal species (and thus `similar`) may have changed while this fetch
      // was in flight; a late response for a species no longer in the list must
      // not repopulate the pruned cache.
      if (!similar.some(e => e.scientific_name === name)) return;
      guides.set(name, extractCanonicalSections(g.description));
      status.set(name, 'ready');
    } catch (e) {
      if (e instanceof ApiError && e.status === HTTP_NOT_FOUND) {
        status.set(name, 'notfound');
      } else {
        status.set(name, 'error');
        logger.error('Failed to load similar-species guide', e, {
          component: 'SimilarSpeciesPanel',
        });
      }
    }
  }

  function select(name: string): void {
    selected = name;
    // Links-only entries (no comparison prose) render their external_links
    // directly from the /similar response — no per-species guide fetch needed.
    const entry = similar.find(e => e.scientific_name === name);
    if (!entry?.has_guide) return;
    // Only fetch on first selection or to retry a prior transient error. Skip
    // when ready, in flight, or 'notfound' — a 404 is a definitive negative
    // result and re-selecting must not re-hit the rate-limited guide endpoint.
    const s = status.get(name);
    if (s === 'ready' || s === 'loading' || s === 'notfound') return;
    void fetchGuide(name);
  }

  // Reacts to `similar` changing (e.g. the focal species changed). Two jobs:
  //  1. Prune cached guides/status for species no longer in the list so the
  //     per-instance cache stays bounded (~maxSimilarSpecies) over a long
  //     browsing session instead of growing without limit. The cache reads are
  //     untracked so loading a guide doesn't re-run this effect.
  //  2. (Re)auto-select the first guide-bearing species when nothing valid is
  //     selected, so the card is never empty and a stale `selected` can't
  //     surface the previous species' cached sections.
  $effect(() => {
    const names = new Set(similar.map(e => e.scientific_name));

    // Everything below reads/writes `selected` and the cache maps, which must NOT
    // become dependencies of this effect — it should react only to `similar`
    // (the focal species / list) changing, not to the user picking a row (that is
    // handled directly by select()). Running it all untracked keeps the effect
    // from re-firing on every selection and avoids a read-write reactive loop.
    untrack(() => {
      for (const key of [...guides.keys()]) {
        if (!names.has(key)) guides.delete(key);
      }
      for (const key of [...status.keys()]) {
        if (!names.has(key)) status.delete(key);
      }

      if (selected === null || !names.has(selected)) {
        // Prefer a species with a full guide (richer card), but fall back to the
        // first species so links-only entries still auto-select.
        const first = similar.find(e => e.has_guide) ?? similar[0];
        if (first) {
          select(first.scientific_name);
        } else if (selected !== null) {
          selected = null;
        }
      }
    });
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
          {@const name = entry.common_name || entry.scientific_name}
          <li>
            <button
              type="button"
              class="w-full rounded-md px-2 py-1.5 text-left text-sm transition-colors
                {entry.scientific_name === selected
                ? 'bg-primary/10 text-primary font-medium'
                : 'hover:bg-base-200'}"
              title={entry.has_guide
                ? name
                : `${name} · ${t('analytics.species.similar.linksOnly')}`}
              aria-pressed={entry.scientific_name === selected}
              onclick={() => select(entry.scientific_name)}
            >
              <span class="flex items-center gap-1">
                <span class="truncate">{name}</span>
                {#if !entry.has_guide}
                  <!-- Subtle cue: this species offers resource links, not a comparison. -->
                  <ExternalLink
                    class="h-3 w-3 shrink-0 text-base-content/40"
                    aria-label={t('analytics.species.similar.linksOnly')}
                  />
                {/if}
              </span>
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

          {#if !selectedEntry.has_guide}
            <!-- No comparison prose: offer resource links so the selection is useful. -->
            {#if (selectedEntry.external_links ?? []).length > 0}
              <p class="text-sm text-base-content/70 mb-2">
                {t('analytics.species.similar.exploreResources')}
              </p>
              <div class="flex flex-wrap gap-2">
                {#each selectedEntry.external_links ?? [] as link (link.url)}
                  <ExternalLinkBadge {link} />
                {/each}
              </div>
            {:else}
              <p class="text-sm text-base-content/70">
                {t('analytics.species.similar.cardNoGuide')}
              </p>
            {/if}
          {:else if selectedStatus === 'loading'}
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

<script module lang="ts">
  import { fetchRegionCoverageMap } from '$lib/utils/modelsApi';

  // Coverage-map cache shared across every RegionCard instance (grid cells and
  // the featured card), keyed by region slug. It stores the in-flight PROMISE,
  // not just the resolved markup, so several cards mounting for the same slug
  // before the first fetch resolves share one request instead of stampeding it.
  // A rejected fetch is evicted so a later view can retry.
  const mapCache = new Map<string, Promise<string>>();

  // Chip caps per layout, and how far outside the viewport a card starts loading
  // its coverage map.
  const CHIP_CAP_FEATURED = 12;
  const CHIP_CAP_GRID = 6;
  const MAP_PREFETCH_MARGIN = '200px 0px';

  /**
   * Fetch a region's coverage map and annotate the inner SVG as aria-hidden so
   * the wrapper's localized label is the only accessible name, deduplicating
   * concurrent and repeat requests via the module cache. This does NOT sanitize
   * the markup: XSS safety rests on the map being first-party, pipeline-generated,
   * same-origin content selected by a server-validated slug (see the {@html} sink).
   */
  export function loadRegionCoverageMap(slug: string): Promise<string> {
    const cached = mapCache.get(slug);
    if (cached) return cached;
    const pending = fetchRegionCoverageMap(slug)
      .then(svg => svg.replace(/<svg\b/, '<svg aria-hidden="true"'))
      .catch((err: unknown) => {
        mapCache.delete(slug); // allow a later retry after a failed fetch
        throw err;
      });
    mapCache.set(slug, pending);
    return pending;
  }

  /** Reset the shared coverage-map cache. Used only by tests for isolation. */
  export function clearRegionMapCache(): void {
    mapCache.clear();
  }
</script>

<script lang="ts">
  /**
   * One region presented as a selectable card: a lazily loaded coverage map, the
   * region name, and its core/partial country chips. The whole card is a native
   * radio (visually hidden) inside a label, so it inherits the browser's radio
   * group semantics: arrow-key navigation across the group, checked state, and
   * native disabling, without any custom roving-tabindex keyboard code.
   */
  import { onMount, onDestroy } from 'svelte';

  import { t } from '$lib/i18n';
  import { localizedCountryNames } from '$lib/utils/countryNames';
  import { loggers } from '$lib/utils/logger';
  import type { RegionOption } from '$lib/types/models';

  const logger = loggers.ui;

  interface Props {
    region: RegionOption;
    /** Whether this region is the current selection (drives the radio checked state). */
    selected: boolean;
    /** Shared radio group name; every card and the Worldwide card use the same name. */
    name: string;
    /** Locale for country-name localization. */
    locale: string;
    /** Disable selection while settings load or save. */
    disabled?: boolean;
    /** 'grid' (default, vertical) or 'featured' (horizontal, larger). */
    layout?: 'grid' | 'featured';
    /** Lazy-load the map on viewport intersection (grid). Featured passes false. */
    lazy?: boolean;
    /** Interactive (radio) card, or a display-only card with no radio. */
    interactive?: boolean;
    onSelect?: (_slug: string) => void;
  }

  let {
    region,
    selected,
    name,
    locale,
    disabled = false,
    layout = 'grid',
    lazy = true,
    interactive = true,
    onSelect,
  }: Props = $props();

  // Country chips. Countries ship with every region, so no fetch is needed here;
  // only the map is lazy. Core chips come first, then partial, capped to keep the
  // grid cells uniform; the remainder collapses into a single "+N" chip whose
  // title lists the hidden countries so they stay readable. Core vs partial is
  // carried by a visually-hidden group label (not colour alone) plus the chip
  // styling.
  const chipCap = $derived(layout === 'featured' ? CHIP_CAP_FEATURED : CHIP_CAP_GRID);
  const coreNames = $derived(localizedCountryNames(region.countries?.core, locale));
  const partialNames = $derived(localizedCountryNames(region.countries?.partial, locale));
  const coreShown = $derived(coreNames.slice(0, chipCap));
  const partialShown = $derived(partialNames.slice(0, Math.max(0, chipCap - coreShown.length)));
  const overflowNames = $derived([
    ...coreNames.slice(coreShown.length),
    ...partialNames.slice(partialShown.length),
  ]);
  const overflowCount = $derived(overflowNames.length);

  // --- Lazy coverage map ----------------------------------------------------
  let shouldLoad = $state(false);
  let coverageSvg = $state<string | null>(null);
  let mapLoading = $state(false);
  let mapFailed = $state(false);
  let cardEl = $state<HTMLElement | undefined>();
  // eslint-disable-next-line no-undef -- browser global
  let observer: IntersectionObserver | undefined;

  onMount(() => {
    if (!lazy) {
      shouldLoad = true;
      return;
    }
    if (!cardEl) {
      shouldLoad = true; // no element to observe: load rather than never showing
      return;
    }
    // eslint-disable-next-line no-undef -- browser global
    observer = new IntersectionObserver(
      entries => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            shouldLoad = true;
            observer?.disconnect(); // one-shot: the map does not need re-observing
            break;
          }
        }
      },
      { rootMargin: MAP_PREFETCH_MARGIN }
    );
    observer.observe(cardEl);
  });

  onDestroy(() => observer?.disconnect());

  $effect(() => {
    const slug = region.slug;
    if (!shouldLoad || !slug) return;

    let cancelled = false;
    mapLoading = true;
    mapFailed = false;
    loadRegionCoverageMap(slug)
      .then(svg => {
        if (cancelled) return;
        coverageSvg = svg;
        mapLoading = false;
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        coverageSvg = null;
        mapFailed = true;
        mapLoading = false;
        logger.error('Failed to load region coverage map', err, {
          component: 'RegionCard',
          slug,
        });
      });

    return () => {
      cancelled = true;
    };
  });
</script>

<svelte:element
  this={interactive ? 'label' : 'div'}
  bind:this={cardEl}
  class="region-card {layout === 'featured' ? 'region-card-featured' : 'region-card-grid'}"
  class:region-card-interactive={interactive}
  class:region-card-selected={selected}
  class:region-card-disabled={disabled}
>
  {#if interactive}
    <input
      type="radio"
      class="sr-only"
      {name}
      value={region.slug}
      checked={selected}
      {disabled}
      aria-label={region.name}
      onchange={() => {
        if (!disabled) onSelect?.(region.slug);
      }}
    />
  {/if}

  <div
    class="region-card-map"
    role="img"
    aria-label={mapFailed
      ? t('analysis.gallery.region.mapUnavailable')
      : t('analysis.gallery.region.mapAria', { region: region.name })}
  >
    {#if mapLoading}
      <div class="region-card-map-skeleton" aria-hidden="true"></div>
    {:else if coverageSvg}
      <!-- Trusted, pipeline-generated, same-origin embedded SVG selected by a
           server-validated slug; the inner SVG is aria-hidden above. -->
      {@html coverageSvg}
    {:else if mapFailed}
      <div class="region-card-map-fallback">
        {t('analysis.gallery.region.mapUnavailable')}
      </div>
    {/if}
  </div>

  <div class="region-card-meta">
    <div class="region-card-top">
      <span class="region-card-name">{region.name}</span>
      {#if selected}
        <span class="badge badge-primary badge-sm"
          >{t('analysis.gallery.region.selectedBadge')}</span
        >
      {/if}
    </div>
    <div class="region-card-chips">
      {#if coreShown.length > 0}
        <span class="sr-only">{t('analysis.gallery.region.coreCoverage')}</span>
        {#each coreShown as country (country)}
          <span class="region-chip region-chip-core">{country}</span>
        {/each}
      {/if}
      {#if partialShown.length > 0}
        <span class="sr-only">{t('analysis.gallery.region.partialCoverage')}</span>
        {#each partialShown as country (country)}
          <span class="region-chip region-chip-partial">{country}</span>
        {/each}
      {/if}
      {#if overflowCount > 0}
        <span class="region-chip region-chip-more" title={overflowNames.join(', ')}
          >{t('analysis.gallery.region.countriesOverflow', { count: overflowCount })}</span
        >
      {/if}
    </div>
  </div>
</svelte:element>

<style>
  .region-card {
    display: flex;
    background: var(--color-base-100);
    border: 1px solid var(--color-base-300);
    border-radius: 0.75rem;
    overflow: hidden;
    transition:
      border-color 0.14s,
      box-shadow 0.14s;
  }

  .region-card-interactive {
    cursor: pointer;
  }

  .region-card-grid {
    flex-direction: column;
  }

  .region-card-featured {
    flex-direction: row;
    gap: 0.75rem;
    padding: 0.75rem;
    align-items: stretch;
  }

  .region-card-interactive:hover {
    border-color: var(--color-primary);
  }

  /* Focus ring on the card follows the visually-hidden native radio. */
  .region-card:has(input:focus-visible) {
    outline: 2px solid var(--color-primary);
    outline-offset: 1px;
  }

  .region-card-selected {
    border-color: var(--color-primary);
    box-shadow: 0 0 0 1px var(--color-primary);
  }

  .region-card-disabled {
    cursor: not-allowed;
    opacity: 0.55;
  }

  .region-card-map {
    aspect-ratio: 4 / 3;
    background: var(--color-base-200);
    overflow: hidden;
  }

  .region-card-grid .region-card-map {
    border-bottom: 1px solid var(--color-base-200);
  }

  .region-card-featured .region-card-map {
    flex: 0 0 220px;
    aspect-ratio: 4 / 3;
    border-radius: 0.5rem;
    border: 1px solid var(--color-base-200);
  }

  .region-card-map :global(svg) {
    display: block;
    width: 100%;
    height: 100%;
  }

  .region-card-map-skeleton {
    width: 100%;
    height: 100%;
    background: linear-gradient(
      90deg,
      var(--color-base-200) 25%,
      var(--color-base-300) 37%,
      var(--color-base-200) 63%
    );
    background-size: 400% 100%;
    animation: region-card-shimmer 1.4s ease infinite;
  }

  @keyframes region-card-shimmer {
    0% {
      background-position: 100% 0;
    }

    100% {
      background-position: 0 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .region-card-map-skeleton {
      animation: none;
    }
  }

  .region-card-map-fallback {
    display: grid;
    place-items: center;
    width: 100%;
    height: 100%;
    padding: 0.5rem;
    font-size: 0.72rem;
    text-align: center;
    color: color-mix(in srgb, var(--color-base-content) 60%, transparent);
  }

  .region-card-meta {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.55rem 0.6rem 0.65rem;
    min-width: 0;
  }

  .region-card-featured .region-card-meta {
    justify-content: center;
    padding: 0;
  }

  .region-card-top {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    justify-content: space-between;
  }

  .region-card-name {
    font-size: 0.875rem;
    font-weight: 600;
  }

  .region-card-featured .region-card-name {
    font-size: 1.15rem;
  }

  .region-card-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  .region-chip {
    font-size: 0.7rem;
    line-height: 1.35;
    padding: 0.1rem 0.5rem;
    border-radius: 1rem;
    white-space: nowrap;
  }

  .region-chip-core {
    background: color-mix(in srgb, var(--color-primary) 15%, transparent);
    color: var(--color-primary);
  }

  .region-chip-partial {
    background: var(--color-base-200);
    color: color-mix(in srgb, var(--color-base-content) 65%, transparent);

    /* Non-colour cue (inset ring, no layout shift) so core vs partial is not
       distinguished by colour alone. */
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-base-content) 28%, transparent);
  }

  .region-chip-more {
    background: var(--color-base-200);
    color: color-mix(in srgb, var(--color-base-content) 55%, transparent);
  }

  /* Theme the inlined coverage SVG to the app's data-theme, not only the OS
     prefers-color-scheme the SVG ships with. These rules MUST be unlayered and
     target .cov directly: the SVG's own \3c style> sets the --cov-* vars on a bare
     .cov{} rule, so an ancestor-scoped or @layer'd override would lose the
     cascade (see acoustic-models coverage-maps-integration.md). :global keeps
     Svelte from scoping them away. */
  :global([data-theme='dark'] .cov) {
    --cov-ocean: #0e1a22;
    --cov-land: #243039;
    --cov-core: #3fae7b;
    --cov-periphery: #2c5f47;
    --cov-border: #0e1a22;
  }

  :global([data-theme='light'] .cov) {
    --cov-ocean: #e9f0f4;
    --cov-land: #dfe3e8;
    --cov-core: #2f9e6b;
    --cov-periphery: #bfe3d0;
    --cov-border: #ffffff;
  }
</style>

<script lang="ts">
  /**
   * Region selector for the model gallery. Lets the user choose how a regional
   * model variant is picked: Automatic (from the station location), Global (the
   * worldwide model), or a pinned region. It is recommend-only: it never installs
   * or switches a model, it only records the preference that install/switch flows
   * read later.
   *
   * The "why-line" explains the current resolution. It derives from the SELECTED
   * mode, not from response.resolved.source: the endpoint always returns the
   * automatic-mode preview in `resolved` regardless of the saved mode, so the
   * pinned/global explanations must come from the mode the user chose.
   */
  import { onMount } from 'svelte';
  import { Globe, MapPin } from '@lucide/svelte';

  import { t, getLocale } from '$lib/i18n';
  import { fetchModelRegions, fetchRegionCoverageMap } from '$lib/utils/modelsApi';
  import { localizedCountryNames } from '$lib/utils/countryNames';
  import { normalizeRegionMode } from '$lib/utils/variantSelection';
  import { loggers } from '$lib/utils/logger';
  import { birdnetSettings, settingsActions } from '$lib/stores/settings';
  import type { ModelRegionsResponse, RegionOption } from '$lib/types/models';

  const logger = loggers.ui;

  interface Props {
    /** Disable all controls while settings are loading or saving. */
    disabled?: boolean;
  }

  let { disabled = false }: Props = $props();

  let data = $state<ModelRegionsResponse | null>(null);
  let loading = $state(true);
  let error = $state(false);
  // User-toggled disclosure of the region list. The list is also shown whenever a
  // specific region is pinned, so the pinned tile is visible without a click.
  let showRegions = $state(false);

  async function load() {
    loading = true;
    error = false;
    try {
      data = await fetchModelRegions();
    } catch (err) {
      error = true;
      logger.error('Failed to load model regions', err, { component: 'ModelRegionSelector' });
    } finally {
      loading = false;
    }
  }

  onMount(load);

  // '' and absent both mean automatic (mirrors the Go omitempty field); see
  // normalizeRegionMode.
  const selected = $derived(normalizeRegionMode($birdnetSettings?.modelRegion));
  const regions = $derived<RegionOption[]>(data?.regions ?? []);
  const isPinnedSlug = $derived(selected !== 'auto' && selected !== 'global');
  const regionsVisible = $derived(showRegions || isPinnedSlug);

  function knownSlug(slug: string): boolean {
    return regions.some(r => r.slug === slug);
  }

  function regionName(slug: string): string {
    return regions.find(r => r.slug === slug)?.name ?? slug;
  }

  // Regions arrive pre-sorted by group display, then name, then slug, so grouping
  // consecutive options preserves the server ordering.
  const groupedRegions = $derived.by(() => {
    const groups: { slug: string; display: string; options: RegionOption[] }[] = [];
    let current: { slug: string; display: string; options: RegionOption[] } | null = null;
    for (const opt of regions) {
      // Break on the continental group slug (a stable unique key), not the
      // display name. A slug maps to exactly one display, and the server sorts by
      // display then name then slug, so same-slug options stay contiguous.
      if (!current || current.slug !== opt.group) {
        current = { slug: opt.group, display: opt.groupDisplay, options: [] };
        groups.push(current);
      }
      current.options.push(opt);
    }
    return groups;
  });

  function select(value: string) {
    settingsActions.updateSection('birdnet', { modelRegion: value });
  }

  // Full i18n keys as literals so the usage checker sees each one.
  const WHY_KEY = {
    noLocation: 'analysis.gallery.region.why.noLocation',
    outsideCoverage: 'analysis.gallery.region.why.outsideCoverage',
    ambiguous: 'analysis.gallery.region.why.ambiguous',
    resolved: 'analysis.gallery.region.why.resolved',
    global: 'analysis.gallery.region.why.global',
    pinned: 'analysis.gallery.region.why.pinned',
    pinnedUnknown: 'analysis.gallery.region.why.pinnedUnknown',
  } as const;

  type WhyState = keyof typeof WHY_KEY;

  interface WhyLine {
    state: WhyState;
    args: Record<string, unknown>; // matches t()'s params type; region names interpolate as strings
    warn: boolean;
    pins: string[]; // region slugs offered as one-click pin shortcuts
    offerAuto: boolean; // show the "switch to automatic" escape hatch
    mismatch?: string; // resolved region name for the secondary "location resolves to" line
  }

  const whyLine = $derived.by<WhyLine | null>(() => {
    if (!data) return null;
    const r = data.resolved;
    const lc = data.locationConfigured;
    const sel = selected;

    if (sel === 'auto') {
      if (!lc) return { state: 'noLocation', args: {}, warn: true, pins: [], offerAuto: false };
      if (r.slug === '')
        return { state: 'outsideCoverage', args: {}, warn: false, pins: [], offerAuto: false };
      if (r.ambiguous) {
        const runner = r.runnerUp ?? '';
        return {
          state: 'ambiguous',
          args: { region: regionName(r.slug), runnerUp: regionName(runner) },
          warn: false,
          pins: runner ? [r.slug, runner] : [r.slug],
          offerAuto: false,
        };
      }
      return {
        state: 'resolved',
        args: { region: regionName(r.slug) },
        warn: false,
        pins: [],
        offerAuto: false,
      };
    }

    if (sel === 'global') {
      return { state: 'global', args: {}, warn: false, pins: [], offerAuto: false };
    }

    // A pinned slug the current catalog does not know about (e.g. dropped upstream).
    if (!knownSlug(sel)) {
      return {
        state: 'pinnedUnknown',
        args: { region: sel },
        warn: true,
        pins: [],
        offerAuto: true,
      };
    }

    // A known pinned region. Surface a secondary line when the location resolves
    // elsewhere, so the user understands the pin is overriding auto-detection.
    const mismatch = lc && r.slug !== '' && r.slug !== sel ? regionName(r.slug) : undefined;
    return {
      state: 'pinned',
      args: { region: regionName(sel) },
      warn: false,
      pins: [],
      offerAuto: false,
      mismatch,
    };
  });

  // --- Active-region detail: coverage map + localized country list ----------
  // The "active region" is the one whose map and country list we surface: the
  // auto-resolved region (once a location is configured), or a known pinned
  // slug. Global, no-location, outside-coverage, and unknown pins have no map.
  const activeSlug = $derived.by<string>(() => {
    if (!data) return '';
    if (selected === 'global') return '';
    if (selected === 'auto') return data.locationConfigured ? data.resolved.slug : '';
    return knownSlug(selected) ? selected : '';
  });

  const activeRegion = $derived<RegionOption | undefined>(
    activeSlug ? regions.find(r => r.slug === activeSlug) : undefined
  );

  const locale = $derived(getLocale());
  const coreCountries = $derived<string[]>(
    localizedCountryNames(activeRegion?.countries?.core, locale)
  );
  const partialCountries = $derived<string[]>(
    localizedCountryNames(activeRegion?.countries?.partial, locale)
  );

  // Country lists can be long; show a capped set with a single expand toggle that
  // governs both the core and the partial list, so neither can render an
  // unbounded comma-joined line.
  const COUNTRY_LIMIT = 8;
  let countriesExpanded = $state(false);
  const coreVisible = $derived(
    countriesExpanded ? coreCountries : coreCountries.slice(0, COUNTRY_LIMIT)
  );
  const partialVisible = $derived(
    countriesExpanded ? partialCountries : partialCountries.slice(0, COUNTRY_LIMIT)
  );
  const hiddenCountries = $derived(
    Math.max(0, coreCountries.length - COUNTRY_LIMIT) +
      Math.max(0, partialCountries.length - COUNTRY_LIMIT)
  );

  // The inlined SVG map for the active region. Fetched lazily per slug and cached
  // so toggling between two regions does not refetch. mapFailed drives the
  // text-only fallback; the country list renders regardless.
  let coverageSvg = $state<string | null>(null);
  let mapLoading = $state(false);
  let mapFailed = $state(false);
  const mapCache = new Map<string, string>();

  $effect(() => {
    const slug = activeSlug;
    // Reset the country disclosure whenever the active region changes.
    countriesExpanded = false;

    if (!slug) {
      coverageSvg = null;
      mapLoading = false;
      mapFailed = false;
      return;
    }

    const cached = mapCache.get(slug);
    if (cached !== undefined) {
      coverageSvg = cached;
      mapLoading = false;
      mapFailed = false;
      return;
    }

    // Guard against out-of-order responses: a slower earlier fetch must not
    // overwrite the map for a region the user has since switched away from.
    let cancelled = false;
    coverageSvg = null;
    mapFailed = false;
    mapLoading = true;

    fetchRegionCoverageMap(slug)
      .then(svg => {
        // aria-hidden the inlined SVG so the wrapper's localized label is the
        // only accessible name (the SVG ships a baked English aria-label and
        // role="img"). Trusted, pipeline-generated, same-origin content. The
        // \b anchor tolerates any root form (`<svg `, `<svg>`, `<svg\n...`).
        const safe = svg.replace(/<svg\b/, '<svg aria-hidden="true"');
        mapCache.set(slug, safe);
        if (!cancelled) {
          coverageSvg = safe;
          mapLoading = false;
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          coverageSvg = null;
          mapFailed = true;
          mapLoading = false;
        }
        logger.error('Failed to load region coverage map', err, {
          component: 'ModelRegionSelector',
          slug,
        });
      });

    return () => {
      cancelled = true;
    };
  });
</script>

<fieldset class="border border-base-300 rounded-lg p-3 mb-4">
  <legend class="text-sm font-medium px-1">{t('analysis.gallery.region.title')}</legend>

  {#if loading}
    <div role="status" class="flex items-center gap-2 text-sm text-base-content/70">
      <span
        class="animate-spin h-4 w-4 border-2 border-primary border-t-transparent rounded-full"
        aria-hidden="true"
      ></span>
      <span>{t('analysis.gallery.region.loading')}</span>
    </div>
  {:else if error}
    <div role="alert" class="flex flex-wrap items-center gap-3 text-sm text-error">
      <span>{t('analysis.gallery.region.loadFailed')}</span>
      <button type="button" class="btn btn-sm btn-ghost" onclick={load}>
        {t('analysis.gallery.retry')}
      </button>
    </div>
  {:else if data}
    <div class="flex flex-col gap-2">
      <!-- Automatic -->
      <label
        for="model-region-auto"
        class="flex items-start gap-3 rounded-md border p-2 transition-colors
          {selected === 'auto' ? 'border-primary bg-primary/5' : 'border-base-300'}
          {disabled ? '' : 'cursor-pointer hover:bg-base-200'}"
      >
        <input
          id="model-region-auto"
          type="radio"
          class="radio radio-sm radio-primary mt-0.5"
          name="model-region"
          value="auto"
          checked={selected === 'auto'}
          {disabled}
          onchange={() => select('auto')}
        />
        <div class="flex flex-col gap-0.5 min-w-0">
          <span class="font-medium flex items-center gap-1.5">
            <MapPin class="h-4 w-4" aria-hidden="true" />
            {t('analysis.gallery.region.modeAuto')}
          </span>
          <span class="text-xs text-base-content/70"
            >{t('analysis.gallery.region.modeAutoHint')}</span
          >
        </div>
      </label>

      <!-- Global -->
      <label
        for="model-region-global"
        class="flex items-start gap-3 rounded-md border p-2 transition-colors
          {selected === 'global' ? 'border-primary bg-primary/5' : 'border-base-300'}
          {disabled ? '' : 'cursor-pointer hover:bg-base-200'}"
      >
        <input
          id="model-region-global"
          type="radio"
          class="radio radio-sm radio-primary mt-0.5"
          name="model-region"
          value="global"
          checked={selected === 'global'}
          {disabled}
          onchange={() => select('global')}
        />
        <div class="flex flex-col gap-0.5 min-w-0">
          <span class="font-medium flex items-center gap-1.5">
            <Globe class="h-4 w-4" aria-hidden="true" />
            {t('analysis.gallery.region.modeGlobal')}
          </span>
          <span class="text-xs text-base-content/70"
            >{t('analysis.gallery.region.modeGlobalHint')}</span
          >
        </div>
      </label>

      <!-- Pinned-region disclosure. The toggle stays mounted (it does not unmount
           on expand), so keyboard focus is never dropped; once a region is pinned
           the list is always shown and the toggle is unnecessary. -->
      {#if !isPinnedSlug}
        <button
          type="button"
          class="btn btn-ghost btn-xs self-start"
          aria-expanded={showRegions}
          aria-controls="model-region-list"
          {disabled}
          onclick={() => (showRegions = !showRegions)}
        >
          {t('analysis.gallery.region.pinLabel')}
        </button>
      {/if}
      {#if regionsVisible}
        <div id="model-region-list" class="flex flex-col gap-2 pl-1">
          {#each groupedRegions as group (group.slug)}
            <div class="flex flex-col gap-1">
              <span class="text-xs font-semibold text-base-content/60 uppercase tracking-wide"
                >{group.display}</span
              >
              {#each group.options as opt (opt.slug)}
                {@const inputId = `model-region-tile-${opt.slug}`}
                <label
                  for={inputId}
                  class="flex items-center gap-3 rounded-md border p-2 transition-colors
                    {selected === opt.slug ? 'border-primary bg-primary/5' : 'border-base-300'}
                    {disabled ? '' : 'cursor-pointer hover:bg-base-200'}"
                >
                  <input
                    id={inputId}
                    type="radio"
                    class="radio radio-sm radio-primary"
                    name="model-region"
                    value={opt.slug}
                    checked={selected === opt.slug}
                    {disabled}
                    onchange={() => select(opt.slug)}
                  />
                  <span class="flex-1 min-w-0">{opt.name}</span>
                  {#if selected === opt.slug}
                    <span class="badge badge-info badge-sm"
                      >{t('analysis.gallery.region.pinnedBadge')}</span
                    >
                  {/if}
                </label>
              {/each}
            </div>
          {/each}
        </div>
      {/if}

      <!-- Why-line: explains the current resolution and offers quick actions. -->
      {#if whyLine}
        <div role="status" class="text-xs {whyLine.warn ? 'text-warning' : 'text-base-content/70'}">
          {t(WHY_KEY[whyLine.state], whyLine.args)}
        </div>
        {#if whyLine.mismatch}
          <div class="text-xs text-base-content/60">
            {t('analysis.gallery.region.why.pinnedMismatch', { resolved: whyLine.mismatch })}
          </div>
        {/if}
        {#if whyLine.pins.length > 0}
          <div class="flex flex-wrap gap-2">
            {#each whyLine.pins as slug (slug)}
              <button type="button" class="btn btn-xs" {disabled} onclick={() => select(slug)}>
                {t('analysis.gallery.region.pinAction', { region: regionName(slug) })}
              </button>
            {/each}
          </div>
        {/if}
        {#if whyLine.offerAuto}
          <div>
            <button type="button" class="btn btn-xs" {disabled} onclick={() => select('auto')}>
              {t('analysis.gallery.region.switchToAuto')}
            </button>
          </div>
        {/if}
      {/if}

      <!-- Active-region detail: coverage map + localized country list. -->
      {#if activeRegion}
        <div class="mt-1 flex flex-col gap-2 rounded-md border border-base-300 p-2">
          {#if mapLoading}
            <div role="status" class="text-xs text-base-content/60">
              {t('analysis.gallery.region.mapLoading')}
            </div>
          {:else if coverageSvg}
            <div
              class="w-full max-w-sm self-center [&_svg]:h-auto [&_svg]:w-full"
              role="img"
              aria-label={t('analysis.gallery.region.mapAria', { region: activeRegion.name })}
            >
              <!-- Trusted, pipeline-generated, same-origin embedded SVG selected by a
                   server-validated slug; the inner SVG is aria-hidden above. -->
              {@html coverageSvg}
            </div>
          {:else if mapFailed}
            <div role="status" class="text-xs text-base-content/70">
              {t('analysis.gallery.region.mapUnavailable')}
            </div>
          {/if}

          {#if coreCountries.length > 0}
            <div class="text-xs text-base-content/70">
              <span class="font-medium"
                >{t('analysis.gallery.region.countriesCore', {
                  countries: coreVisible.join(', '),
                })}</span
              >
            </div>
          {/if}

          {#if partialCountries.length > 0}
            <div class="text-xs text-base-content/60">
              {t('analysis.gallery.region.countriesPartial', {
                countries: partialVisible.join(', '),
              })}
            </div>
          {/if}

          {#if hiddenCountries > 0}
            <button
              type="button"
              class="btn btn-ghost btn-xs self-start"
              aria-expanded={countriesExpanded}
              onclick={() => (countriesExpanded = !countriesExpanded)}
            >
              {countriesExpanded
                ? t('analysis.gallery.region.countriesLess')
                : t('analysis.gallery.region.countriesMore', { count: hiddenCountries })}
            </button>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</fieldset>

<style>
  /* Theme the inlined coverage SVG to the app's data-theme, not only the OS
     prefers-color-scheme the SVG ships with. These rules MUST be unlayered and
     target .cov directly: the SVG's own \3c style> sets the --cov-* vars on a bare
     .cov{} rule, so an ancestor-scoped or @layer'd override would lose the
     cascade (see acoustic-models coverage-maps-integration.md). Svelte injects
     component styles unlayered; :global keeps them from being scoped away. */
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

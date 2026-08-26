<script lang="ts">
  /**
   * Region selector for the model gallery. Two top-level modes: Automatic (pick
   * the regional variant from the station location) and Manual region select
   * (choose a region, or the worldwide model, from a card grid). It is
   * recommend-only: it never installs or switches a model, it only records the
   * `birdnet.modelRegion` preference (`auto`/`''`, `global`, or a region slug)
   * that install/switch flows read later.
   *
   * "Worldwide" is folded into the manual list as its first, special card
   * (writing `global`); there is no separate top-level Global option.
   *
   * The why-line explains the current resolution. Automatic-mode explanations
   * derive from the SELECTED mode, not from response.resolved.source: the
   * endpoint always returns the automatic-mode preview in `resolved` regardless
   * of the saved mode.
   */
  import { onMount } from 'svelte';
  import { Globe, Layers, MapPin, Search, TriangleAlert } from '@lucide/svelte';

  import { t, getLocale } from '$lib/i18n';
  import { fetchModelRegions } from '$lib/utils/modelsApi';
  import { localizedCountryNames } from '$lib/utils/countryNames';
  import {
    normalizeRegionMode,
    DEFAULT_REGION_MODE,
    GLOBAL_REGION_MODE,
  } from '$lib/utils/variantSelection';
  import { loggers } from '$lib/utils/logger';
  import { birdnetSettings, settingsActions } from '$lib/stores/settings';
  import type { ModelRegionsResponse, RegionOption } from '$lib/types/models';
  import RegionCard from './RegionCard.svelte';

  const logger = loggers.ui;

  const MODE_GROUP = 'model-region-mode';
  const CHOICE_GROUP = 'model-region-choice';

  interface Props {
    /** Disable all controls while settings are loading or saving. */
    disabled?: boolean;
  }

  let { disabled = false }: Props = $props();

  let data = $state<ModelRegionsResponse | null>(null);
  let loading = $state(true);
  let error = $state(false);
  // User's search filter over the manual region grid.
  let searchQuery = $state('');
  // User-toggled intent to browse the manual list. The manual view is also shown
  // whenever a specific region or the worldwide model is saved (so the current
  // choice is visible without a click); see `manualMode`.
  let manualIntent = $state(false);

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
  const isRegionSlug = $derived(
    selected !== DEFAULT_REGION_MODE && selected !== GLOBAL_REGION_MODE
  );
  const isGlobal = $derived(selected === GLOBAL_REGION_MODE);
  // Manual view is active on explicit intent, or whenever the saved value is a
  // region or the worldwide model.
  const manualMode = $derived(manualIntent || isRegionSlug || isGlobal);

  // Reconcile the local manual intent with external store changes: if the saved
  // region reverts to automatic from a region/global value (e.g. the user hits
  // "Discard changes" in the page header), collapse back to the Automatic view.
  // Known minor edge: if the user only toggles Manual (nothing picked yet, so the
  // saved value is still 'auto') and then discards, `selected` never changes, so
  // this effect cannot fire and the view stays in Manual. This is harmless: no
  // value was changed to discard, and the persisted 'auto' is correct; detecting
  // it would require coupling to the page's dirty/original state.
  // `prevSelected` is tracked only inside the effect (undefined on first run) so
  // the initial value is not read reactively in instance scope.
  let prevSelected: string | undefined;
  $effect(() => {
    const cur = selected;
    if (
      prevSelected !== undefined &&
      prevSelected !== DEFAULT_REGION_MODE &&
      cur === DEFAULT_REGION_MODE
    ) {
      manualIntent = false;
    }
    prevSelected = cur;
  });

  const locale = $derived(getLocale());

  function knownSlug(slug: string): boolean {
    return regions.some(r => r.slug === slug);
  }

  function regionName(slug: string): string {
    return regions.find(r => r.slug === slug)?.name ?? slug;
  }

  function select(value: string) {
    settingsActions.updateSection('birdnet', { modelRegion: value });
  }

  function chooseAutomatic() {
    manualIntent = false;
    select(DEFAULT_REGION_MODE);
  }

  // --- Search ---------------------------------------------------------------
  // Fold diacritics so "espana" matches "España" and "reunion" matches "Réunion".
  function foldForSearch(s: string): string {
    return s
      .normalize('NFD')
      .replace(/[\u0300-\u036f]/g, '')
      .toLowerCase();
  }

  const foldedQuery = $derived(foldForSearch(searchQuery.trim()));

  // Searchable haystack per region: its name, continental group, and ALL of its
  // localized country names (core and partial), so a match on a country hidden
  // inside the "+N" overflow still surfaces the card. Precomputed once per
  // regions/locale change (NOT per keystroke): the country localization is the
  // expensive part and does not depend on the query.
  const regionHaystacks = $derived.by(() => {
    const map = new Map<string, string>();
    for (const opt of regions) {
      const countries = localizedCountryNames(
        [...(opt.countries?.core ?? []), ...(opt.countries?.partial ?? [])],
        locale
      );
      map.set(opt.slug, foldForSearch([opt.name, opt.groupDisplay, ...countries].join(' ')));
    }
    return map;
  });

  const filteredRegions = $derived(
    foldedQuery
      ? regions.filter(opt => (regionHaystacks.get(opt.slug) ?? '').includes(foldedQuery))
      : regions
  );

  // Regions arrive pre-sorted by group display, then name, then slug, so grouping
  // consecutive options preserves the server ordering.
  function groupByContinent(list: RegionOption[]) {
    const groups: { slug: string; display: string; options: RegionOption[] }[] = [];
    let current: { slug: string; display: string; options: RegionOption[] } | null = null;
    for (const opt of list) {
      if (!current || current.slug !== opt.group) {
        current = { slug: opt.group, display: opt.groupDisplay, options: [] };
        groups.push(current);
      }
      current.options.push(opt);
    }
    return groups;
  }

  const filteredGroups = $derived(groupByContinent(filteredRegions));
  const noResults = $derived(foldedQuery.length > 0 && filteredRegions.length === 0);

  // --- Automatic-view resolution detail -------------------------------------
  // The resolved region shown as a featured card in the Automatic view: the
  // auto-resolved known region once a location is configured. No-location,
  // outside-coverage, and unknown resolutions have no featured card.
  const resolvedRegion = $derived.by<RegionOption | undefined>(() => {
    if (!data || !data.locationConfigured) return undefined;
    const slug = data.resolved.slug;
    if (!slug || !knownSlug(slug)) return undefined;
    return regions.find(r => r.slug === slug);
  });

  // Automatic-view why-line. Full i18n keys as literals so the usage checker sees
  // each one.
  const AUTO_WHY = {
    noLocation: 'analysis.gallery.region.why.noLocation',
    outsideCoverage: 'analysis.gallery.region.why.outsideCoverage',
    ambiguous: 'analysis.gallery.region.why.ambiguous',
    resolved: 'analysis.gallery.region.why.resolved',
  } as const;

  interface AutoWhy {
    state: keyof typeof AUTO_WHY;
    args: Record<string, unknown>;
    warn: boolean;
    pins: string[]; // region slugs offered as one-click select shortcuts
  }

  const autoWhy = $derived.by<AutoWhy | null>(() => {
    if (!data || manualMode) return null;
    const r = data.resolved;
    if (!data.locationConfigured) return { state: 'noLocation', args: {}, warn: true, pins: [] };
    if (r.slug === '') return { state: 'outsideCoverage', args: {}, warn: false, pins: [] };
    if (r.ambiguous) {
      const runner = r.runnerUp ?? '';
      return {
        state: 'ambiguous',
        args: { region: regionName(r.slug), runnerUp: regionName(runner) },
        warn: false,
        pins: runner ? [r.slug, runner] : [r.slug],
      };
    }
    return { state: 'resolved', args: { region: regionName(r.slug) }, warn: false, pins: [] };
  });

  // Manual-view status line for the current selection.
  interface ManualStatus {
    key: string;
    args: Record<string, unknown>;
    warn: boolean;
    mismatch?: string; // resolved region name for the secondary "location resolves to" line
    offerAuto: boolean; // show the "switch to automatic" escape hatch
  }

  const manualStatus = $derived.by<ManualStatus | null>(() => {
    if (!data || !manualMode) return null;
    if (isGlobal) {
      return { key: 'analysis.gallery.region.why.global', args: {}, warn: false, offerAuto: false };
    }
    if (isRegionSlug) {
      if (!knownSlug(selected)) {
        return {
          key: 'analysis.gallery.region.why.pinnedUnknown',
          args: { region: selected },
          warn: true,
          offerAuto: true,
        };
      }
      const r = data.resolved;
      const mismatch =
        data.locationConfigured && r.slug !== '' && r.slug !== selected
          ? regionName(r.slug)
          : undefined;
      return {
        key: 'analysis.gallery.region.why.pinned',
        args: { region: regionName(selected) },
        warn: false,
        mismatch,
        offerAuto: false,
      };
    }
    // Manual intent, nothing chosen yet.
    return { key: 'analysis.gallery.region.manualPrompt', args: {}, warn: false, offerAuto: false };
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
      <!-- Mode: Automatic / Manual region select. Native radios share a `name`,
           so they form one group; the fieldset legend names it (no redundant
           role="radiogroup"/aria-label, which would double-announce). -->
      <div class="flex flex-col gap-2">
        <!-- Automatic -->
        <label
          for="model-region-auto"
          class="flex items-start gap-3 rounded-md border p-2 transition-colors
            {!manualMode ? 'border-primary bg-primary/5' : 'border-base-300'}
            {disabled ? '' : 'cursor-pointer hover:bg-base-200'}"
        >
          <input
            id="model-region-auto"
            type="radio"
            class="radio radio-sm radio-primary mt-0.5"
            name={MODE_GROUP}
            value="auto"
            checked={!manualMode}
            {disabled}
            onchange={chooseAutomatic}
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

        <!-- Manual region select -->
        <label
          for="model-region-manual"
          class="flex items-start gap-3 rounded-md border p-2 transition-colors
            {manualMode ? 'border-primary bg-primary/5' : 'border-base-300'}
            {disabled ? '' : 'cursor-pointer hover:bg-base-200'}"
        >
          <input
            id="model-region-manual"
            type="radio"
            class="radio radio-sm radio-primary mt-0.5"
            name={MODE_GROUP}
            value="manual"
            checked={manualMode}
            {disabled}
            onchange={() => (manualIntent = true)}
          />
          <div class="flex flex-col gap-0.5 min-w-0">
            <span class="font-medium flex items-center gap-1.5">
              <Layers class="h-4 w-4" aria-hidden="true" />
              {t('analysis.gallery.region.modeManual')}
            </span>
            <span class="text-xs text-base-content/70"
              >{t('analysis.gallery.region.modeManualHint')}</span
            >
          </div>
        </label>
      </div>

      {#if !manualMode}
        <!-- Automatic view: resolved region as a featured card + why-line. -->
        {#if resolvedRegion}
          <RegionCard
            region={resolvedRegion}
            selected={false}
            name={CHOICE_GROUP}
            {locale}
            layout="featured"
            lazy={false}
            interactive={false}
          />
        {/if}
        {#if autoWhy}
          <div
            role="status"
            class="text-xs {autoWhy.warn ? 'text-warning' : 'text-base-content/70'}"
          >
            {t(AUTO_WHY[autoWhy.state], autoWhy.args)}
          </div>
          {#if autoWhy.pins.length > 0}
            <div class="flex flex-wrap gap-2">
              {#each autoWhy.pins as slug (slug)}
                <button type="button" class="btn btn-xs" {disabled} onclick={() => select(slug)}>
                  {t('analysis.gallery.region.pinAction', { region: regionName(slug) })}
                </button>
              {/each}
            </div>
          {/if}
        {/if}
      {:else}
        <!-- Manual view: search + Worldwide card + region grid. -->
        <div class="relative">
          <Search
            class="h-4 w-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-base-content/50 pointer-events-none"
            aria-hidden="true"
          />
          <input
            type="search"
            class="input input-sm input-bordered w-full pl-8"
            placeholder={t('analysis.gallery.region.search')}
            aria-label={t('analysis.gallery.region.search')}
            bind:value={searchQuery}
            {disabled}
          />
        </div>

        <div
          role="radiogroup"
          aria-label={t('analysis.gallery.region.modeManual')}
          class="flex flex-col gap-3"
        >
          <!-- Worldwide: the special first card, no coverage map. -->
          <label
            for="model-region-worldwide"
            class="flex items-start gap-3 rounded-lg border p-3 transition-colors
              has-[:focus-visible]:outline has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-primary
              {isGlobal
              ? 'border-primary shadow-[0_0_0_1px_var(--color-primary)]'
              : 'border-base-300'}
              {disabled ? 'opacity-55 cursor-not-allowed' : 'cursor-pointer hover:border-primary'}"
          >
            <input
              id="model-region-worldwide"
              type="radio"
              class="sr-only"
              name={CHOICE_GROUP}
              value="global"
              checked={isGlobal}
              {disabled}
              onchange={() => {
                if (!disabled) select(GLOBAL_REGION_MODE);
              }}
            />
            <!-- Center the icon with flex, NOT the `grid` class: a global
                 `.drawer-content .grid { width: 100% }` rule would otherwise stretch
                 this fixed-size box to full width and push the text out of the card. -->
            <span
              class="flex-none flex items-center justify-center h-12 w-16 rounded-md bg-primary/10 text-primary"
              aria-hidden="true"
            >
              <Globe class="h-7 w-7" />
            </span>
            <div class="flex flex-col gap-1 min-w-0">
              <div class="flex items-center gap-2">
                <span class="font-semibold">{t('analysis.gallery.region.worldwideTitle')}</span>
                {#if isGlobal}
                  <span class="badge badge-primary badge-sm"
                    >{t('analysis.gallery.region.selectedBadge')}</span
                  >
                {/if}
              </div>
              <span class="text-xs text-base-content/70"
                >{t('analysis.gallery.region.worldwideSubtitle')}</span
              >
              <span
                class="inline-flex items-center gap-1 self-start rounded px-1.5 py-0.5 text-xs font-medium text-warning bg-warning/10"
              >
                <TriangleAlert class="h-3.5 w-3.5" aria-hidden="true" />
                {t('analysis.gallery.region.worldwideResourceNote')}
              </span>
            </div>
          </label>

          <div class="text-[0.7rem] uppercase tracking-wide text-base-content/50 px-0.5">
            {t('analysis.gallery.region.orSpecificRegion')}
          </div>

          {#if noResults}
            <div role="status" class="text-xs text-base-content/60 py-2">
              {t('analysis.gallery.region.searchNoResults', { query: searchQuery.trim() })}
            </div>
          {:else}
            {#each filteredGroups as group (group.slug)}
              <div class="flex flex-col gap-2">
                <span
                  class="text-xs font-semibold text-base-content/60 uppercase tracking-wide px-0.5"
                  >{group.display}</span
                >
                <div class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(200px,1fr))]">
                  {#each group.options as opt (opt.slug)}
                    <RegionCard
                      region={opt}
                      selected={selected === opt.slug}
                      name={CHOICE_GROUP}
                      {locale}
                      {disabled}
                      onSelect={select}
                    />
                  {/each}
                </div>
              </div>
            {/each}
          {/if}
        </div>

        {#if manualStatus}
          <div
            role="status"
            class="text-xs {manualStatus.warn ? 'text-warning' : 'text-base-content/70'}"
          >
            {t(manualStatus.key, manualStatus.args)}
          </div>
          {#if manualStatus.mismatch}
            <div class="text-xs text-base-content/60">
              {t('analysis.gallery.region.why.pinnedMismatch', { resolved: manualStatus.mismatch })}
            </div>
          {/if}
          {#if manualStatus.offerAuto}
            <div>
              <button type="button" class="btn btn-xs" {disabled} onclick={chooseAutomatic}>
                {t('analysis.gallery.region.switchToAuto')}
              </button>
            </div>
          {/if}
        {/if}
      {/if}
    </div>
  {/if}
</fieldset>

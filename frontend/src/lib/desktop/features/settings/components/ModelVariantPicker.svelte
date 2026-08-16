<script lang="ts">
  /**
   * Hardware/regional variant picker for the model gallery install dialog. The
   * recommended variant is preselected by the caller (see pickPreselectedVariant);
   * this component surfaces why it is recommended, lets the user override, and
   * explains any incompatible option instead of silently disabling it.
   *
   * With regional models an entry can carry ~80 variants (globals plus a tile per
   * region), so the options are revealed in stages rather than all at once: the
   * always-relevant ones first, then the global builds plus the active region's
   * tiles, then every remaining region. The disclosure labels never over-promise
   * (a filtering stage is never called "all"), and a context line names the region
   * the middle stage scopes to, so the list is never a silently filtered subset.
   */
  import { untrack } from 'svelte';
  import type { CatalogVariant, VariantReason } from '$lib/types/models';
  import { t } from '$lib/i18n';
  import { formatBytes, formatNumber } from '$lib/utils/formatters';
  import { translateReason, topReasons, variantLabel } from '$lib/utils/variantSelection';

  interface Props {
    variants: CatalogVariant[];
    installedVariantId?: string;
    selectedVariantId: string;
    // eslint-disable-next-line no-unused-vars -- the param name documents the callback contract
    onSelect: (id: string) => void;
    /** Disable all options while an install is in flight. */
    disabled?: boolean;
    /** Unique prefix for radio input ids so multiple pickers do not collide. */
    idPrefix: string;
    /**
     * The active region slug (the saved region, or the auto-resolved one), used to
     * scope the middle disclosure stage. '' when no region is active.
     */
    activeRegionSlug?: string;
    /** slug -> localized region name, so a regional variant reads as the selector names it. */
    regionNames?: ReadonlyMap<string, string>;
  }

  let {
    variants,
    installedVariantId,
    selectedVariantId,
    onSelect,
    disabled = false,
    idPrefix,
    activeRegionSlug = '',
    regionNames,
  }: Props = $props();

  const hasRecommended = $derived(variants.some(v => v.recommended));
  const hasRegionalVariants = $derived(variants.some(v => v.region));
  const activeRegionName = $derived(regionNames?.get(activeRegionSlug) ?? activeRegionSlug);

  // The always-relevant options, shown in every stage: the recommended one, the
  // installed one, and the current selection, so the smart default and any manual
  // override are never hidden behind a disclosure.
  function inAlways(variant: CatalogVariant): boolean {
    return (
      variant.recommended ||
      variant.installed ||
      variant.id === selectedVariantId ||
      variant.id === installedVariantId
    );
  }

  // Progressive disclosure in three stages. 'collapsed' shows only the
  // always-relevant options; 'region' adds the global builds and the active
  // region's tiles; 'all' adds every remaining region. Starts collapsed when a
  // recommendation exists (the smart default is what the user sees first),
  // otherwise at 'region' so a regional entry does not open as an ~80-row wall.
  type Disclosure = 'collapsed' | 'region' | 'all';
  // Initial stage only: the dialog remounts this picker on each open (licenseModel
  // toggles null between opens), so capturing the mount-time recommendation state
  // is intentional. untrack makes that explicit and avoids a reactive dependency.
  let disclosure = $state<Disclosure>(untrack(() => (hasRecommended ? 'collapsed' : 'region')));

  // The 'region' stage: always-relevant options, plus every global (region-less)
  // build, plus the active region's tiles. Other regions are excluded unless one
  // is itself always-relevant (e.g. an installed tile from another region).
  const regionStageVariants = $derived(
    variants.filter(v => inAlways(v) || !v.region || v.region === activeRegionSlug)
  );
  // The tiles the 'all' stage adds after the 'region' set: other regions not
  // already always-relevant. Rendered under the "Other regions" heading.
  const otherStageVariants = $derived(
    variants.filter(v => !inAlways(v) && !!v.region && v.region !== activeRegionSlug)
  );

  // The first (or only) group of tiles rendered for the current stage. In the
  // 'all' stage this is still the region-stage set; otherStageVariants render
  // after it under their own heading.
  const visibleVariants = $derived(
    disclosure === 'collapsed' ? variants.filter(inAlways) : regionStageVariants
  );

  // The disclosure button for the current stage, or null when everything is shown.
  // Counts are the number of rows the click reveals, so the label never promises
  // more than it delivers, and a filtering stage is never labeled "all".
  interface DisclosureStep {
    labelKey: string;
    count: number;
    target: Disclosure;
    region?: string;
  }
  const disclosureStep = $derived.by<DisclosureStep | null>(() => {
    const alwaysCount = variants.filter(inAlways).length;
    if (disclosure === 'collapsed') {
      const allReveal = variants.length - alwaysCount;
      if (allReveal <= 0) return null;
      const regionReveal = regionStageVariants.length - alwaysCount;
      // When no other-region tiles are hidden, or the region stage would reveal
      // nothing beyond the always-relevant set, collapse to a single honest jump
      // straight to every variant, using the existing "Show all variants" label.
      if (otherStageVariants.length === 0 || regionReveal <= 0) {
        return { labelKey: 'analysis.gallery.variants.showAll', count: allReveal, target: 'all' };
      }
      if (activeRegionSlug) {
        return {
          labelKey: 'analysis.gallery.variants.showRegion',
          count: regionReveal,
          target: 'region',
          region: activeRegionName,
        };
      }
      return {
        labelKey: 'analysis.gallery.variants.showHardware',
        count: regionReveal,
        target: 'region',
      };
    }
    if (disclosure === 'region' && otherStageVariants.length > 0) {
      return {
        labelKey: 'analysis.gallery.variants.showAllRegions',
        count: otherStageVariants.length,
        target: 'all',
      };
    }
    return null;
  });

  // Fall back to the raw reason code so an unmapped reason stays inspectable
  // rather than surfacing a dotted i18n key to the user.
  function reasonText(reason: VariantReason): string {
    return translateReason(reason.code, reason.args, reason.code);
  }

  // The reason shown on a blocked option: its first blocker, or a generic
  // localized line when the server marked it incompatible without a structured
  // reason, so a disabled option is never left silently unexplained.
  function blockedReasonText(variant: CatalogVariant): string {
    const blocker = variant.blockers?.[0];
    return blocker ? reasonText(blocker) : t('analysis.gallery.variants.incompatible');
  }
</script>

{#snippet variantTile(variant: CatalogVariant)}
  {@const inputId = `${idPrefix}-${variant.id}`}
  {@const blocked = !variant.compatible}
  <label
    for={inputId}
    class="flex items-start gap-3 rounded-md border p-2 transition-colors
      {selectedVariantId === variant.id ? 'border-primary bg-primary/5' : 'border-base-300'}
      {blocked ? 'bg-base-200/40' : 'cursor-pointer hover:bg-base-200'}"
  >
    <!-- Blocked options use aria-disabled rather than native disabled so they stay
         in the radio group's roving tab order and a keyboard or screen-reader user
         can focus one to hear its reason (see the aria-describedby line below).
         Native disabled is kept only for the whole-picker in-flight case. Clicks and
         the Space key are suppressed; arrow-key navigation can transiently check a
         blocked radio before onchange fires, so onchange reverts it. Documented
         a11y tradeoff (frontend/CLAUDE.md: prefer aria-disabled + suppressed click). -->
    <input
      id={inputId}
      type="radio"
      class="radio radio-sm radio-primary mt-0.5"
      name="{idPrefix}-variant"
      value={variant.id}
      checked={selectedVariantId === variant.id}
      {disabled}
      aria-disabled={blocked ? 'true' : undefined}
      title={blocked ? blockedReasonText(variant) : undefined}
      aria-describedby={blocked ? `${inputId}-reason` : undefined}
      onclick={e => {
        if (blocked) e.preventDefault();
      }}
      onchange={e => {
        if (blocked) {
          // Arrow-key roving synchronously checks the focused (blocked) radio and
          // unchecks the prior selection before this fires. Revert the blocked
          // radio, then restore the previously-selected one: selectedVariantId did
          // not change, so its one-way checked binding will not re-assert it, and
          // without this the group would be left with nothing visually checked.
          e.currentTarget.checked = false;
          const prev = document.getElementById(`${idPrefix}-${selectedVariantId}`);
          if (prev instanceof HTMLInputElement) prev.checked = true;
          return;
        }
        onSelect(variant.id);
      }}
    />

    <div class="flex flex-col gap-1 min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="font-medium">{variantLabel(variant, regionNames)}</span>
        {#if variant.recommended}
          <span class="badge badge-primary badge-sm"
            >{t('analysis.gallery.variants.recommended')}</span
          >
        {/if}
        {#if variant.installed}
          <span class="badge badge-info badge-sm">{t('analysis.gallery.variants.installed')}</span>
        {/if}
        {#if variant.default}
          <span class="badge badge-ghost badge-sm">{t('analysis.gallery.variants.default')}</span>
        {/if}
      </div>

      <div class="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-base-content/70">
        <span>{formatBytes(variant.sizeBytes)}</span>
        {#if variant.speciesCount > 0}
          <span
            >{t('analysis.gallery.species', {
              count: formatNumber(variant.speciesCount),
            })}</span
          >
        {/if}
        {#if variant.headlineLatencyMs && variant.headlineLatencyMs > 0}
          <span>{t('analysis.gallery.variants.latency', { ms: variant.headlineLatencyMs })}</span>
        {/if}
      </div>

      {#if variant.recommended && variant.reasons?.length}
        {@const recommendedReasons = topReasons(variant.reasons)}
        <!-- Render every surfaced reason as a list item under a labeled heading: a
             list announces "N items" to a screen reader, so the second reason is
             unambiguously a second reason rather than an unrelated line. -->
        <div class="text-xs text-primary/90">
          <p class="font-medium">
            {t('analysis.gallery.variants.recommendedForHardware')}:
          </p>
          <ul class="list-disc pl-4 space-y-0.5">
            {#each recommendedReasons as reason, i (i)}
              <li>{reason}</li>
            {/each}
          </ul>
        </div>
      {/if}

      {#if blocked}
        <p id="{inputId}-reason" class="text-xs text-error">{blockedReasonText(variant)}</p>
      {/if}
    </div>
  </label>
{/snippet}

<fieldset class="border border-base-300 rounded-lg p-3">
  <legend class="text-sm font-medium px-1">{t('analysis.gallery.variants.title')}</legend>

  <!-- Region context: name the region the list is scoped to, so a user always
       knows which region's tiles they are looking at, or how to get one. -->
  {#if hasRegionalVariants && activeRegionSlug}
    <p class="text-xs text-base-content/70 mb-2">
      {t('analysis.gallery.variants.regionContext', { region: activeRegionName })}
    </p>
  {:else if hasRegionalVariants}
    <p class="text-xs text-base-content/70 mb-2">
      {t('analysis.gallery.variants.regionContextNone')}
    </p>
  {/if}

  <!-- Native radios sharing a name inside the fieldset already form a labeled
       radio group, so no explicit role/aria-label is needed here. -->
  <div class="flex flex-col gap-2">
    {#each visibleVariants as variant (variant.id)}
      {@render variantTile(variant)}
    {/each}

    {#if disclosure === 'all' && otherStageVariants.length > 0}
      <p class="text-xs font-medium text-base-content/60 mt-1">
        {t('analysis.gallery.variants.otherRegions')}
      </p>
      {#each otherStageVariants as variant (variant.id)}
        {@render variantTile(variant)}
      {/each}
    {/if}
  </div>

  {#if disclosureStep}
    <!-- The disclosure button only renders while more variants remain hidden (it
         unmounts once everything is shown), so it always reveals collapsed
         content: aria-expanded is a constant "false". -->
    <button
      type="button"
      class="btn btn-ghost btn-xs mt-2"
      aria-expanded="false"
      onclick={() => (disclosure = disclosureStep.target)}
    >
      {disclosureStep.region
        ? t(disclosureStep.labelKey, { count: disclosureStep.count, region: disclosureStep.region })
        : t(disclosureStep.labelKey, { count: disclosureStep.count })}
    </button>
  {/if}
</fieldset>

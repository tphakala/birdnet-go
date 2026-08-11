<script lang="ts">
  /**
   * Hardware/regional variant picker for the model gallery install dialog. The
   * recommended variant is preselected by the caller (see pickPreselectedVariant);
   * this component surfaces why it is recommended, lets the user override, and
   * explains any incompatible option instead of silently disabling it.
   */
  import type { CatalogVariant, VariantReason } from '$lib/types/models';
  import { t } from '$lib/i18n';
  import { formatBytes, formatNumber } from '$lib/utils/formatters';
  import { reasonKey, variantLabel } from '$lib/utils/variantSelection';

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
  }

  let {
    variants,
    installedVariantId,
    selectedVariantId,
    onSelect,
    disabled = false,
    idPrefix,
  }: Props = $props();

  const hasRecommended = $derived(variants.some(v => v.recommended));

  // Progressive disclosure: when a recommended variant exists, collapse the
  // other non-installed options so the smart default is what the user sees
  // first, with manual override one click away.
  let showAll = $state(false);

  function alwaysShown(variant: CatalogVariant): boolean {
    return (
      variant.recommended ||
      variant.installed ||
      variant.id === selectedVariantId ||
      variant.id === installedVariantId
    );
  }

  const visibleVariants = $derived(
    !hasRecommended || showAll ? variants : variants.filter(alwaysShown)
  );
  const hiddenCount = $derived(variants.length - visibleVariants.length);

  function reasonText(reason: VariantReason): string {
    const key = reasonKey(reason.code);
    const translated = t(key, reason.args);
    // t returns the key itself when there is no translation; fall back to the raw
    // reason code so an unmapped reason stays inspectable rather than surfacing a
    // dotted i18n key to the user.
    return translated === key ? reason.code : translated;
  }

  // The reason shown on a blocked option: its first blocker, or a generic
  // localized line when the server marked it incompatible without a structured
  // reason, so a disabled option is never left silently unexplained.
  function blockedReasonText(variant: CatalogVariant): string {
    const blocker = variant.blockers?.[0];
    return blocker ? reasonText(blocker) : t('analysis.gallery.variants.incompatible');
  }
</script>

<fieldset class="border border-base-300 rounded-lg p-3">
  <legend class="text-sm font-medium px-1">{t('analysis.gallery.variants.title')}</legend>

  <!-- Native radios sharing a name inside the fieldset already form a labeled
       radio group, so no explicit role/aria-label is needed here. -->
  <div class="flex flex-col gap-2">
    {#each visibleVariants as variant (variant.id)}
      {@const inputId = `${idPrefix}-${variant.id}`}
      {@const blocked = !variant.compatible}
      <label
        for={inputId}
        class="flex items-start gap-3 rounded-md border p-2 transition-colors
          {selectedVariantId === variant.id ? 'border-primary bg-primary/5' : 'border-base-300'}
          {blocked ? 'bg-base-200/40' : 'cursor-pointer hover:bg-base-200'}"
      >
        <input
          id={inputId}
          type="radio"
          class="radio radio-sm radio-primary mt-0.5"
          name="{idPrefix}-variant"
          value={variant.id}
          checked={selectedVariantId === variant.id}
          disabled={disabled || blocked}
          title={blocked ? blockedReasonText(variant) : undefined}
          aria-describedby={blocked ? `${inputId}-reason` : undefined}
          onchange={() => onSelect(variant.id)}
        />

        <div class="flex flex-col gap-1 min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium">{variantLabel(variant)}</span>
            {#if variant.recommended}
              <span class="badge badge-primary badge-sm"
                >{t('analysis.gallery.variants.recommended')}</span
              >
            {/if}
            {#if variant.installed}
              <span class="badge badge-info badge-sm"
                >{t('analysis.gallery.variants.installed')}</span
              >
            {/if}
            {#if variant.default}
              <span class="badge badge-ghost badge-sm"
                >{t('analysis.gallery.variants.default')}</span
              >
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
              <span
                >{t('analysis.gallery.variants.latency', { ms: variant.headlineLatencyMs })}</span
              >
            {/if}
          </div>

          {#if variant.recommended && variant.reasons?.length}
            <p class="text-xs text-primary/90">
              {t('analysis.gallery.variants.recommendedForHardware')}: {reasonText(
                variant.reasons[0]
              )}
            </p>
          {/if}

          {#if blocked}
            <p id="{inputId}-reason" class="text-xs text-error">{blockedReasonText(variant)}</p>
          {/if}
        </div>
      </label>
    {/each}
  </div>

  {#if hasRecommended && hiddenCount > 0 && !showAll}
    <button
      type="button"
      class="btn btn-ghost btn-xs mt-2"
      aria-expanded={showAll}
      onclick={() => (showAll = true)}
    >
      {t('analysis.gallery.variants.showAll', { count: hiddenCount })}
    </button>
  {/if}
</fieldset>

<script lang="ts">
  /**
   * RareSpeciesIndicator
   *
   * Shows a red star with a tooltip when a species is "rare" for the configured
   * location and time of year, i.e. its geomodel occurrence probability is at or
   * below the configurable Dashboard.Rarity.Threshold. Renders nothing when the
   * feature is disabled, scores are unavailable, or the species is not rare.
   *
   * The occurrence probability is the same value shown as Species Rarity on the
   * detection detail page. Species without geomodel coverage (e.g. bats) are never
   * flagged (see rarity.svelte.ts).
   */
  import { Star } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { dashboardSettings, DEFAULT_RARITY } from '$lib/stores/settings';
  import { isRareSpecies, getOccurrence, loadRarityScores } from '$lib/stores/rarity.svelte';

  interface Props {
    /** Scientific name of the detected species. */
    scientificName: string | undefined;
    /** Tailwind size class for the icon (defaults to the daily-summary star size). */
    sizeClass?: string;
  }

  let { scientificName, sizeClass = 'size-3' }: Props = $props();

  const PERCENT_MULTIPLIER = 100;

  let enabled = $derived($dashboardSettings?.rarity?.enabled ?? DEFAULT_RARITY.enabled);
  let threshold = $derived($dashboardSettings?.rarity?.threshold ?? DEFAULT_RARITY.threshold);

  // Load the occurrence-score cache once the feature is on (dedupes internally).
  $effect(() => {
    if (enabled) {
      void loadRarityScores();
    }
  });

  let rare = $derived(enabled && isRareSpecies(scientificName, threshold));
  let occurrence = $derived(getOccurrence(scientificName));
  let percent = $derived(
    occurrence !== undefined ? Math.round(occurrence * PERCENT_MULTIPLIER) : undefined
  );

  // Parenthetical tooltip to match sibling indicators, e.g. "Rare (12% occurrence)".
  let tooltip = $derived(
    percent !== undefined
      ? t('species.rarity.highlightTooltip', { percent })
      : t('species.rarity.statuses.rare')
  );
</script>

{#if rare}
  <span
    class="inline-block shrink-0 text-[var(--color-error)]"
    role="img"
    title={tooltip}
    aria-label={tooltip}
  >
    <Star class="{sizeClass} fill-current" />
  </span>
{/if}

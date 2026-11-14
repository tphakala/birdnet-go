<!--
  IdentityCard - what was detected and when: species photo, names, taxonomy,
  status chips, confidence, the button that opens the model's other
  predictions, and a row with the date, time and source.

  The card has one height for every detection, so the review controls under it
  stay where they are while stepping through detections. That height comes from
  content that is always there: each text area is one line, cut with an ellipsis
  when it does not fit, and no line is held empty in case text wraps. A long
  species name is set in smaller type instead of taking a second line.
-->
<script lang="ts">
  import { Calendar, Camera, Clock, ListTree, Moon, Sun, Sunrise, Sunset } from '@lucide/svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import VerificationBadges from '$lib/desktop/components/ui/VerificationBadges.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import { t } from '$lib/i18n';
  import type { ImageAttribution } from '$lib/types/detection.types';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import DetailCard from './DetailCard.svelte';
  import { formatPercent, toPercent } from './detailFormat';
  import { formatCalendarDate, type SunOffset } from './detailTime';
  import type { DetectionDetailData, SpeciesRarity, TaxonomyHierarchy } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    displayName: string;
    taxonomy?: TaxonomyHierarchy | null;
    rarity?: SpeciesRarity | null;
    imageAttribution?: ImageAttribution | null;
    sunOffset?: SunOffset | null;
    /** How many other species the model predicted; null while that is not known. */
    predictionCount: number | null;
    /** Opens the list of the model's other predictions. */
    onShowPredictions: () => void;
  }

  let {
    detection,
    displayName,
    taxonomy = null,
    rarity = null,
    imageAttribution = null,
    sunOffset = null,
    predictionCount,
    onShowPredictions,
  }: Props = $props();

  const TAXONOMY_SEPARATOR = ' · ';
  // Names longer than these are set one and two type sizes smaller, so more of the
  // name fits on its one line.
  const LONG_NAME_LENGTH = 15;
  const VERY_LONG_NAME_LENGTH = 22;

  let nameSize = $derived.by(() => {
    if (displayName.length > VERY_LONG_NAME_LENGTH) return 'small';
    if (displayName.length > LONG_NAME_LENGTH) return 'medium';
    return 'large';
  });

  // The family and its common name first, then order and class: when the line is
  // cut short, what remains is the part that tells most about the species. The
  // genus is left out, as it is the first word of the scientific name above.
  let taxonomyText = $derived(
    taxonomy
      ? [taxonomy.family, taxonomy.family_common, taxonomy.order, taxonomy.class]
          .filter((part): part is string => Boolean(part))
          .join(TAXONOMY_SEPARATOR)
      : ''
  );

  // True when a reviewer replaced the model's species with another.
  let speciesSetByReviewer = $derived(
    detection.originalSpecies !== undefined &&
      detection.originalSpecies.scientificName !== detection.scientificName
  );

  // Any other time of day (night, dusk, dawn) shows the moon.
  const TIME_OF_DAY_ICONS = new Map<string, typeof Sun>([
    ['day', Sun],
    ['sunrise', Sunrise],
    ['sunset', Sunset],
  ]);

  let timeOfDay = $derived(detection.timeOfDay?.toLowerCase() ?? '');
  let FactIcon = $derived(TIME_OF_DAY_ICONS.get(timeOfDay) ?? Moon);

  let sunOffsetText = $derived.by(() => {
    if (!sunOffset) return '';
    const { event, before, hours, minutes } = sunOffset;
    if (hours === 0) {
      if (event === 'sunrise') {
        return before
          ? t('detections.detail.identity.minutesBeforeSunrise', { minutes })
          : t('detections.detail.identity.minutesAfterSunrise', { minutes });
      }
      return before
        ? t('detections.detail.identity.minutesBeforeSunset', { minutes })
        : t('detections.detail.identity.minutesAfterSunset', { minutes });
    }
    if (event === 'sunrise') {
      return before
        ? t('detections.detail.identity.beforeSunrise', { hours, minutes })
        : t('detections.detail.identity.afterSunrise', { hours, minutes });
    }
    return before
      ? t('detections.detail.identity.beforeSunset', { hours, minutes })
      : t('detections.detail.identity.afterSunset', { hours, minutes });
  });

  // "Sunrise, 26 min before sunrise" says the same thing twice, so around
  // sunrise and sunset the offset stands alone.
  let timeOfDayText = $derived.by(() => {
    if (!timeOfDay) return '';
    const label = t(`detections.timeOfDay.${timeOfDay}`);
    if (!sunOffsetText) return label;
    if (timeOfDay === 'sunrise' || timeOfDay === 'sunset') return sunOffsetText;
    return `${label}, ${sunOffsetText}`;
  });

  let hasPredictions = $derived(predictionCount !== null && predictionCount > 0);

  let predictionsLabel = $derived.by(() => {
    if (predictionCount === null) return t('detections.detail.identity.predictionsUnknown');
    if (predictionCount === 0) return t('detections.detail.identity.predictionsNone');
    return t('detections.detail.identity.predictionsCount', { count: predictionCount });
  });

  function showPredictions() {
    if (hasPredictions) onShowPredictions();
  }
</script>

<DetailCard padding="even">
  <div class="dd-identity">
    <div class="dd-identity-upper">
      <div class="dd-identity-photo">
        <img
          src={buildAppUrl(
            `/api/v2/media/species-image?name=${encodeURIComponent(detection.scientificName)}`
          )}
          alt={displayName}
          onerror={handleBirdImageError}
          loading="eager"
        />
        {#if imageAttribution?.authorName}
          <div class="dd-identity-credit">
            <Camera size={10} aria-hidden="true" />
            <span class="dd-identity-credit-author">{imageAttribution.authorName}</span>
            {#if imageAttribution.licenseName}
              <span aria-hidden="true">·</span>
              {#if imageAttribution.licenseURL}
                <a href={imageAttribution.licenseURL} target="_blank" rel="noopener noreferrer"
                  >{imageAttribution.licenseName}</a
                >
              {:else}
                <span>{imageAttribution.licenseName}</span>
              {/if}
            {/if}
          </div>
        {/if}
      </div>

      <div class="dd-identity-main">
        <div class="dd-identity-head">
          <div class="dd-identity-names">
            <h1
              id="species-heading"
              class="dd-identity-name dd-identity-name-{nameSize}"
              title={displayName}
            >
              {displayName}
              <span class="sr-only">{t('detections.detail.aria.speciesHeadingSuffix')}</span>
            </h1>
            <p class="dd-identity-scientific">
              <span class="sr-only">{t('detections.detail.aria.scientificName')}: </span>
              {detection.scientificName}
            </p>
            <!-- Always rendered: its line does not depend on the taxonomy request. -->
            <p class="dd-identity-taxonomy" title={taxonomyText}>
              {#if taxonomyText}
                <span class="sr-only">{t('species.taxonomy.hierarchy')}: </span>{taxonomyText}
              {/if}
            </p>
          </div>

          <div class="dd-identity-confidence">
            <!-- The ring shows the value as a check mark at 100%, so the value is read
                 from the text beside it instead. -->
            <span class="dd-identity-ring" aria-hidden="true">
              <ConfidenceCircle confidence={detection.confidence} size="xl" />
            </span>
            <span class="sr-only">
              {t('detections.detail.aria.confidence', {
                confidence: toPercent(detection.confidence),
              })}
            </span>
            <span class="dd-identity-confidence-label" aria-hidden="true"
              >{t('detections.detail.identity.confidence')}</span
            >
          </div>
        </div>

        <div
          class="dd-identity-chips"
          role="group"
          aria-label={t('detections.detail.aria.classificationBadges')}
        >
          <VerificationBadges {detection} size="sm" />
          {#if rarity && rarity.status !== 'unknown'}
            <span class="dd-chip dd-chip-rarity dd-chip-rarity-{rarity.status}">
              {t(`species.rarity.statuses.${rarity.status}`)}
              <span class="dd-chip-value">{formatPercent(rarity.score)}</span>
            </span>
          {/if}
          {#if speciesSetByReviewer}
            <span class="dd-chip">{t('detections.detail.identity.setByReviewer')}</span>
          {/if}
          {#if detection.isNewSpecies}
            <span class="dd-chip dd-chip-new">{t('species.tracking.newSpecies')}</span>
          {:else if detection.isNewThisYear}
            <span class="dd-chip dd-chip-new">{t('species.tracking.newThisYear')}</span>
          {:else if detection.isNewThisSeason}
            <span class="dd-chip dd-chip-new">{t('species.tracking.newThisSeason')}</span>
          {/if}
        </div>

        <!-- Always in this place. Without other predictions it stays, blocked, and
             its label says why. -->
        <div class="dd-identity-actions">
          <Button
            variant="default"
            size="sm"
            aria-haspopup="dialog"
            aria-disabled={hasPredictions ? undefined : 'true'}
            onclick={showPredictions}
          >
            <ListTree class="size-3.5" />
            {predictionsLabel}
          </Button>
        </div>
      </div>
    </div>

    <!-- Four cells in a fixed grid; a fact that was not recorded leaves its cell empty, so
         the card keeps its height. -->
    <dl class="dd-identity-facts">
      <div class="dd-fact">
        <dt>
          <Calendar class="size-3.5" /><span class="sr-only"
            >{t('detections.detail.identity.date')}</span
          >
        </dt>
        <dd>{formatCalendarDate(detection.date)}</dd>
      </div>
      <div class="dd-fact">
        <dt>
          <Clock class="size-3.5" /><span class="sr-only"
            >{t('detections.detail.identity.time')}</span
          >
        </dt>
        <dd class="dd-fact-time">{detection.time}</dd>
      </div>
      {#if timeOfDayText}
        <div class="dd-fact dd-fact-grow">
          <dt>
            <FactIcon class="size-3.5" />
            <span class="sr-only">{t('detections.detail.identity.timeOfDay')}</span>
          </dt>
          <dd class="dd-fact-text" title={timeOfDayText}>{timeOfDayText}</dd>
        </div>
      {/if}
      {#if detection.source}
        <div class="dd-fact dd-fact-source">
          <dt class="sr-only">{t('detections.metadata.source')}</dt>
          <dd><SourceBadge {detection} variant="inline" /></dd>
        </div>
      {/if}
    </dl>
  </div>
</DetailCard>

<style>
  .dd-identity {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    min-width: 0;
    container: dd-identity / inline-size;
  }

  .dd-identity-upper {
    display: flex;
    gap: 1.25rem;
    min-width: 0;
  }

  /* Most species pictures are 4:3. The box has that shape and shows the whole
     picture; one of another shape is fitted inside it, not cropped. */
  .dd-identity-photo {
    position: relative;
    flex: 0 0 auto;
    align-self: flex-start;
    width: 13.5rem;
    aspect-ratio: 4 / 3;
    overflow: hidden;
    border-radius: calc(var(--radius-box) - 0.25rem);
    background-color: var(--color-base-200);
  }

  .dd-identity-photo img {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .dd-identity-credit {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.75rem 0.5rem 0.25rem;
    font-size: 0.5625rem;
    color: rgb(255 255 255 / 0.9);

    /* Dark enough under the text line for white text on a pale photo; it fades out above it. */
    background: linear-gradient(to top, rgb(0 0 0 / 0.75), rgb(0 0 0 / 0.65) 60%, transparent);
  }

  .dd-identity-credit-author {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-identity-credit a {
    white-space: nowrap;
    text-decoration: underline;
  }

  .dd-identity-main {
    display: flex;
    flex: 1;
    flex-direction: column;
    justify-content: space-between;
    gap: 0.5rem;
    min-width: 0;
  }

  .dd-identity-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    min-width: 0;
  }

  .dd-identity-names {
    flex: 1;
    min-width: 0;
  }

  /* One line of a fixed height in every type size, so a long name changes the
     size of the letters and not of the card. */
  .dd-identity-name {
    height: 2.25rem;
    overflow: hidden;
    font-weight: 700;
    line-height: 2.25rem;
    letter-spacing: -0.02em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-identity-name-large {
    font-size: 1.875rem;
  }

  .dd-identity-name-medium {
    font-size: 1.5rem;
  }

  .dd-identity-name-small {
    font-size: 1.25rem;
  }

  .dd-identity-scientific {
    overflow: hidden;
    font-size: 1rem;
    font-style: italic;
    line-height: 1.5rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-subtle);
  }

  .dd-identity-taxonomy {
    height: 1.125rem;
    margin-top: 0.25rem;
    overflow: hidden;
    font-size: 0.75rem;
    line-height: 1.125rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-subtle);
  }

  .dd-identity-confidence {
    display: flex;
    flex: 0 0 auto;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
  }

  /* The ring is decorative here; the wrapper only hides it, so it adds no box. */
  .dd-identity-ring {
    display: contents;
  }

  .dd-identity-confidence-label {
    font-size: 0.625rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-subtle);
  }

  /* One row. Chips that do not fit are cut at the edge of the card. */
  .dd-identity-chips {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.375rem;
    height: 1.625rem;
    overflow: hidden;
  }

  .dd-identity-chips > :global(*) {
    flex: 0 0 auto;
  }

  .dd-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
    padding: 0.125rem 0.5rem;
    font-size: 0.75rem;
    font-weight: 500;
    line-height: 1.25rem;
    white-space: nowrap;
    border: 1px solid var(--border-200);
    border-radius: var(--radius-full);
    background-color: var(--color-base-200);
  }

  .dd-chip-value {
    font-variant-numeric: tabular-nums;
    opacity: 0.8;
  }

  .dd-chip-rarity-rare,
  .dd-chip-rarity-very_rare {
    color: var(--text-warning);
    border-color: color-mix(in srgb, var(--color-warning) 45%, transparent);
    background-color: color-mix(in srgb, var(--color-warning) 14%, transparent);
  }

  .dd-chip-new {
    color: var(--text-success);
    border-color: color-mix(in srgb, var(--color-success) 45%, transparent);
    background-color: color-mix(in srgb, var(--color-success) 14%, transparent);
  }

  .dd-identity-actions {
    display: flex;
    flex: 0 0 auto;
  }

  /* Date, time, time of day and source. In a wide card they are one row; in a
     narrow one, two rows of two. Either way every row holds facts. */
  .dd-identity-facts {
    display: grid;
    grid-template-rows: repeat(2, var(--dd-fact-height));
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.25rem 1.25rem;
    padding-top: 0.625rem;
    font-size: 0.875rem;
    border-top: 1px solid var(--border-100);

    --dd-fact-height: 1.625rem;
  }

  @container dd-identity (min-width: 38rem) {
    .dd-identity-facts {
      grid-template-rows: var(--dd-fact-height);
      grid-template-columns: auto auto minmax(0, 1fr) auto;
    }
  }

  .dd-fact {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    min-width: 0;
    height: var(--dd-fact-height);
    white-space: nowrap;
  }

  /* The last cell, also when the time of day before it is missing. */
  .dd-fact-source {
    grid-column-end: -1;
  }

  .dd-fact dt {
    display: inline-flex;
    flex: 0 0 auto;
    color: var(--text-subtle);
  }

  .dd-fact dd {
    min-width: 0;
  }

  .dd-fact-time {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }

  .dd-fact-text {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  @media (max-width: 900px) {
    .dd-identity-photo {
      width: 10.5rem;
    }
  }
</style>

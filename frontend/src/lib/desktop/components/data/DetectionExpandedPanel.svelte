<!--
  DetectionExpandedPanel.svelte

  Purpose: The contents of an expanded detection row - weather, species image with
  reference links, and the audio player with its "compare sounds" link. Shared by
  Search's results list and the analytics Summary page's recent detections list so
  the two never drift into different layouts or link sets.

  Props:
  - detectionId: detection identifier (number or string, normalised for the APIs)
  - scientificName / commonName: species names as stored (English common name)
  - displayName: localized common name shown to the user
  - hasAudio: whether a clip exists for this detection
  - timestamp: detection timestamp, used to label extracted clips
  - modelType: model that produced the detection, forwarded to the audio player
  - rowId: id of the expanded <tr>, referenced by the image's aria-controls
  - onCollapse: called when the image is activated, collapsing the row again
-->
<script lang="ts">
  import WeatherInfo from '$lib/desktop/components/data/WeatherInfo.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import { getLocale, t } from '$lib/i18n';
  import { dashboardSettings } from '$lib/stores/settings';
  import type { TemperatureUnit } from '$lib/utils/formatters';
  import { isAuthenticated } from '$lib/utils/auth';
  import {
    getAllAboutBirdsSoundsUrl,
    getAllAboutBirdsUrl,
    getWikipediaUrl,
    hasSpeciesReferenceName,
  } from '$lib/utils/speciesLinks';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { ExternalLink } from '@lucide/svelte';

  interface Props {
    detectionId: number | string;
    scientificName: string;
    commonName: string;
    displayName: string;
    hasAudio: boolean;
    timestamp?: string;
    modelType?: string;
    rowId: string;
    onCollapse: () => void;
  }

  let {
    detectionId,
    scientificName,
    commonName,
    displayName,
    hasAudio,
    timestamp,
    modelType,
    rowId,
    onCollapse,
  }: Props = $props();

  const id = $derived(String(detectionId));
  const speciesLabel = $derived(displayName || t('search.detailsPanel.unknownSpecies'));
  // A blank common name cannot address a guide page, so the reference links are
  // dropped rather than pointed at a URL that 404s.
  const hasReferenceLinks = $derived(hasSpeciesReferenceName(commonName));

  // Map user's temperature preference to TemperatureUnit format
  // Settings store uses 'celsius'/'fahrenheit', but formatters use 'metric'/'imperial'/'standard'
  const temperatureUnits = $derived.by((): TemperatureUnit => {
    const setting = $dashboardSettings?.temperatureUnit;
    if (setting === 'fahrenheit') return 'imperial';
    return 'metric'; // Default to metric (Celsius)
  });

  // Clip extraction requires authentication
  const clipExtractionEnabled = $derived($isAuthenticated);
</script>

<div class="expanded-details-grid">
  <!-- Weather Information Container -->
  <div class="bg-[var(--color-base-200)] rounded-box p-4">
    <WeatherInfo detectionId={id} units={temperatureUnits} />
  </div>

  <!-- Bird Image Container (Middle Column) -->
  <div class="bg-[var(--color-base-200)] rounded-box p-4 flex flex-col justify-center items-center">
    <div
      class="w-full aspect-[4/3] rounded-md overflow-hidden bg-gray-100 cursor-pointer hover:brightness-90 transition-all focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]"
      onclick={onCollapse}
      onmousedown={e => {
        // Suppress the persistent focus ring on a plain click; keyboard Tab still shows it.
        e.preventDefault();
      }}
      onkeydown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onCollapse();
        }
      }}
      role="button"
      tabindex="0"
      aria-label={t('search.detailsPanel.collapseDetails', { species: speciesLabel })}
      aria-expanded={true}
      aria-controls={rowId}
      title={t('search.detailsPanel.clickToCollapse')}
    >
      <img
        src={buildAppUrl(`/api/v2/media/species-image?name=${encodeURIComponent(scientificName)}`)}
        alt={speciesLabel}
        class="w-full h-full object-cover"
        onload={e => {
          (e.currentTarget as HTMLImageElement).classList.remove('p-2');
        }}
        onerror={e => {
          (e.currentTarget as HTMLImageElement).classList.add('p-2');
          handleBirdImageError(e);
        }}
        loading="lazy"
        decoding="async"
        fetchpriority="low"
      />
    </div>
    {#if hasReferenceLinks}
      <div class="species-reference-links">
        <a
          href={getAllAboutBirdsUrl(commonName)}
          target="_blank"
          rel="noopener noreferrer"
          class="media-compare-link"
          aria-label={t('detections.detail.aria.viewOnAllAboutBirds', { name: speciesLabel })}
        >
          <ExternalLink class="w-3.5 h-3.5" />
          <span>{t('detections.media.viewOnAllAboutBirds')}</span>
        </a>
        <a
          href={getWikipediaUrl(displayName || commonName, getLocale(), commonName)}
          target="_blank"
          rel="noopener noreferrer"
          class="media-compare-link"
          aria-label={t('detections.detail.aria.viewOnWikipedia', { name: speciesLabel })}
        >
          <span class="species-reference-wikipedia-icon">W</span>
          <span>{t('detections.media.viewOnWikipedia')}</span>
        </a>
      </div>
    {/if}
  </div>

  <!-- Audio Player (shown only when this detection has a clip) -->
  {#if hasAudio}
    <div class="bg-[var(--color-base-200)] rounded-box p-4 min-w-0">
      <div class="expanded-audio-heading-row">
        <h3 class="text-lg font-semibold">
          {t('search.detailsPanel.audioPlayer')}
        </h3>
        {#if hasReferenceLinks}
          <a
            href={getAllAboutBirdsSoundsUrl(commonName)}
            target="_blank"
            rel="noopener noreferrer"
            class="media-compare-link"
            aria-label={t('detections.detail.aria.compareSounds', { name: speciesLabel })}
          >
            <ExternalLink class="w-3.5 h-3.5" />
            <span>{t('detections.media.compareSounds')}</span>
          </a>
        {/if}
      </div>
      <AudioPlayer
        audioUrl={buildAppUrl(`/api/v2/audio/${id}`)}
        detectionId={id}
        showDownload={true}
        showSpectrogram={true}
        responsive={true}
        spectrogramSize="lg"
        className="w-full"
        enableClipExtraction={clipExtractionEnabled}
        clipLabel={`${commonName}_${(timestamp ?? '').replace(/[: ]/g, '-')}`}
        {modelType}
      />
    </div>
  {/if}
</div>

<style>
  /* Expanded detection details: weather stays narrow, the audio player/spectrogram
     gets the extra room it needs so the waveform isn't squeezed. */
  .expanded-details-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
  }

  /* 1025px, not 1024px: at exactly 1024px the global tablet rule switches
     tables to table-layout: fixed, which would squeeze three columns into a
     cell that is no longer sized by its content.
     minmax(0, ...) keeps every track shrinkable - bare fr tracks take their
     min-content as a floor, which in an auto-layout table inflates the whole
     table (horizontal scrolling) and, under fixed layout, starves the last
     track until the audio player spills out of its box. */
  @media (min-width: 1025px) {
    .expanded-details-grid {
      grid-template-columns: minmax(0, 0.8fr) minmax(0, 1fr) minmax(0, 1.6fr);
    }
  }

  .expanded-audio-heading-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-bottom: 0.5rem;
  }

  .media-compare-link {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--color-base-content);
    opacity: 0.75;
    text-decoration: none;
    white-space: nowrap;
  }

  .media-compare-link:hover {
    opacity: 1;
    text-decoration: underline;
  }

  .species-reference-links {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-wrap: wrap;
    gap: 1rem;
    margin-top: 0.75rem;
  }

  .species-reference-wikipedia-icon {
    font-family: serif;
    font-weight: 700;
    font-size: 0.75rem;
    line-height: 1;
  }
</style>

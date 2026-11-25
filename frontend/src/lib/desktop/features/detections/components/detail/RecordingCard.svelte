<!--
  RecordingCard - the saved recording: its facts (capture window, length,
  format, noise reduction, file name), a plainly named download button, and the
  player with its spectrogram. After retention removed the audio, the kept
  spectrogram image takes the player's place.

  The player's own Export menu saves a processed copy or a selected range in a
  chosen format. The download button is the simple path, one click: the
  recording as the player plays it by default, so with the default playback
  noise reduction applied when one is set.
-->
<script lang="ts">
  import { Download } from '@lucide/svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import SpectrogramImage from '$lib/desktop/components/media/SpectrogramImage.svelte';
  import { t } from '$lib/i18n';
  import type { PlaybackControls } from '$lib/utils/audio';
  import { isAuthenticated } from '$lib/utils/auth';
  import type { ShortcutHint } from '$lib/utils/keyboard';
  import { audioUrlFor, playbackDenoiseDefaultOf } from '$lib/utils/playbackDenoise';
  import { appState } from '$lib/stores/appState.svelte';
  import { settingsDataLoaded, settingsStore } from '$lib/stores/settings';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import DetailCard from './DetailCard.svelte';
  import { formatDecimal } from './detailFormat';
  import { inOffsetOf, secondsBetween, wallClockTime } from './detailTime';
  import type { DetectionDetailData } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    /** Receives the player's controls when it mounts; the function it returns detaches them. */
    onPlaybackControls?: (_controls: PlaybackControls) => () => void;
    /** The key that plays and pauses, shown on the player's play button. */
    playShortcut?: ShortcutHint | null;
  }

  let { detection, speciesName, onPlaybackControls, playShortcut = null }: Props = $props();

  const HERTZ_PER_KILOHERTZ = 1000;

  // Signed-in users can save a range of the clip; the hint tells them how.
  let clipExtractionEnabled = $derived($isAuthenticated);

  // The download carries the playback denoise default, read reactively so a
  // default saved on this page (the player's playback defaults dialog) updates it.
  let downloadUrl = $derived(
    audioUrlFor(
      detection.id,
      playbackDenoiseDefaultOf(
        $settingsDataLoaded,
        $settingsStore.originalData.realtime,
        appState.playbackDenoise
      )
    )
  );

  let captureWindow = $derived.by(() => {
    // On the detection's own offset, like every other clock on the page.
    const begin = wallClockTime(inOffsetOf(detection.beginTime, detection.timestamp), true);
    const end = wallClockTime(inOffsetOf(detection.endTime, detection.timestamp), true);
    return begin && end ? `${begin}-${end}` : '';
  });

  let lengthSeconds = $derived(secondsBetween(detection.beginTime, detection.endTime));

  let fileName = $derived.by(() => {
    const name = detection.clipName ?? '';
    const dot = name.lastIndexOf('.');
    return dot > 0
      ? { stem: name.slice(0, dot), extension: name.slice(dot) }
      : { stem: name, extension: '' };
  });

  let format = $derived.by(() => {
    const dot = detection.clipName?.lastIndexOf('.') ?? -1;
    const extension =
      dot >= 0 && detection.clipName ? detection.clipName.slice(dot + 1).toUpperCase() : '';
    const rate = detection.clipSampleRate
      ? `${formatDecimal(detection.clipSampleRate / HERTZ_PER_KILOHERTZ, 0)} kHz`
      : '';
    return [extension, rate].filter(Boolean).join(', ');
  });
</script>

<DetailCard>
  {#snippet header()}
    <div class="dd-rec">
      <div class="dd-rec-main">
        <!-- DetailCard styles this class: the title sits in this header's own layout. -->
        <h2 class="dd-card-title">{t('detections.media.title')}</h2>
        <dl class="dd-rec-facts">
          {#if captureWindow}
            <div class="dd-rec-fact">
              <dt>{t('detections.detail.clip.window')}</dt>
              <dd>{captureWindow}</dd>
            </div>
          {/if}
          {#if lengthSeconds !== null && lengthSeconds > 0}
            <div class="dd-rec-fact">
              <dt>{t('detections.detail.clip.length')}</dt>
              <dd>{t('detections.detail.clip.seconds', { count: lengthSeconds })}</dd>
            </div>
          {/if}
          {#if format}
            <div class="dd-rec-fact">
              <dt>{t('detections.detail.clip.format')}</dt>
              <dd>{format}</dd>
            </div>
          {/if}
          {#if detection.clipName && detection.clipDenoised !== undefined}
            <div class="dd-rec-fact">
              <dt>{t('detections.detail.clip.denoised')}</dt>
              <dd>
                {detection.clipDenoised
                  ? t('detections.detail.clip.denoisedYes')
                  : t('detections.detail.clip.denoisedNo')}
              </dd>
            </div>
          {/if}
          {#if detection.clipName}
            <div class="dd-rec-fact dd-rec-fact-file">
              <dt>{t('detections.detail.clip.file')}</dt>
              <!-- A long name is cut before its extension, which always shows. -->
              <dd title={detection.clipName}>
                <span class="dd-rec-file-stem">{fileName.stem}</span>{fileName.extension}
              </dd>
            </div>
          {/if}
        </dl>
        {#if !detection.clipName}
          <p class="dd-rec-note">
            {detection.spectrogramOnly
              ? t('detections.detail.clip.audioRemoved')
              : t('detections.detail.clip.noClip')}
          </p>
        {:else if clipExtractionEnabled}
          <p class="dd-rec-note">{t('detections.media.clipHint')}</p>
        {/if}
      </div>

      {#if detection.clipName}
        <a href={downloadUrl} download class="dd-rec-download focus-ring">
          <Download class="size-4" />
          <span>{t('media.audio.download')}<span class="sr-only">, {speciesName}</span></span>
        </a>
      {/if}
    </div>
  {/snippet}

  <!-- Overflow visible, so the player's menus are not clipped. -->
  <div class="detail-audio-container">
    {#key detection.id}
      {#if detection.clipName}
        <AudioPlayer
          audioUrl={buildAppUrl(`/api/v2/audio/${detection.id}`)}
          detectionId={detection.id.toString()}
          showSpectrogram={true}
          spectrogramSize="lg"
          spectrogramRaw={false}
          responsive={true}
          className="w-full"
          enableClipExtraction={clipExtractionEnabled}
          clipLabel={`${detection.commonName}_${detection.date}_${detection.time.replace(/:/g, '-')}`}
          modelType={detection.modelType}
          clipSampleRate={detection.clipSampleRate}
          spectrogramFollowsDenoise={true}
          {onPlaybackControls}
          {playShortcut}
        />
      {:else}
        <SpectrogramImage
          detectionId={detection.id.toString()}
          size="lg"
          raw={false}
          className="w-full"
        />
      {/if}
    {/key}
  </div>
</DetailCard>

<style>
  .dd-rec {
    display: flex;
    flex: 1;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    min-width: 0;
  }

  .dd-rec-main {
    min-width: 0;
  }

  .dd-rec-facts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1.5rem;
    margin-top: 0.5rem;
    font-size: 0.8125rem;
  }

  .dd-rec-fact {
    display: flex;
    align-items: baseline;
    gap: 0.375rem;
    min-width: 0;
  }

  .dd-rec-fact dt {
    flex: 0 0 auto;
    color: var(--text-subtle);
  }

  .dd-rec-fact dd {
    min-width: 0;
    font-variant-numeric: tabular-nums;
    font-weight: 500;
  }

  /* As wide as the name: the facts wrap, so a long name takes a line of its own and is
     cut only where the card is narrower than the name. */
  .dd-rec-fact-file dd {
    display: flex;
    font-weight: 400;
    white-space: nowrap;
  }

  .dd-rec-file-stem {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dd-rec-note {
    margin-top: 0.375rem;
    font-size: 0.75rem;
    color: var(--text-subtle);
  }

  .dd-rec-download {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4375rem 0.75rem;
    font-size: 0.8125rem;
    font-weight: 500;
    border: 1px solid var(--border-200);
    border-radius: var(--radius-field);
    background-color: var(--color-base-200);
  }

  .dd-rec-download:hover {
    background-color: var(--color-base-300);
  }

  .detail-audio-container {
    position: relative;
    width: 100%;
    overflow: visible;
    border-radius: 0.5rem;
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
  }
</style>

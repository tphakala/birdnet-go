<!--
  RecordingHeader - heading of the recording card: the facts about the saved
  recording (capture window, length, format, noise reduction, file name) and a
  plainly named download button.

  The player's own Export menu saves a processed copy or a selected range in a
  chosen format. This button is the simple path: the saved recording, one click.
-->
<script lang="ts">
  import { Download } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { audioUrlFor, getPlaybackDenoiseDefault } from '$lib/utils/playbackDenoise';
  import { secondsBetween, wallClockTime } from './detailTime';
  import type { DetectionDetailData } from './detailTypes';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    /** Shows the hint about selecting a range on the spectrogram. */
    showClipHint: boolean;
    headingId: string;
  }

  let { detection, speciesName, showClipHint, headingId }: Props = $props();

  const HERTZ_PER_KILOHERTZ = 1000;

  let captureWindow = $derived.by(() => {
    const begin = wallClockTime(detection.beginTime, true);
    const end = wallClockTime(detection.endTime, true);
    return begin && end ? `${begin}-${end}` : '';
  });

  let lengthSeconds = $derived(secondsBetween(detection.beginTime, detection.endTime));

  let format = $derived.by(() => {
    const dot = detection.clipName?.lastIndexOf('.') ?? -1;
    const extension =
      dot >= 0 && detection.clipName ? detection.clipName.slice(dot + 1).toUpperCase() : '';
    const rate = detection.clipSampleRate
      ? `${detection.clipSampleRate / HERTZ_PER_KILOHERTZ} kHz`
      : '';
    return [extension, rate].filter(Boolean).join(', ');
  });
</script>

<header class="dd-rec">
  <div class="dd-rec-main">
    <h2 id={headingId} class="dd-rec-title">{t('detections.media.title')}</h2>
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
          <dd title={detection.clipName}>{detection.clipName}</dd>
        </div>
      {/if}
    </dl>
    {#if !detection.clipName}
      <p class="dd-rec-note">
        {detection.spectrogramOnly
          ? t('detections.detail.clip.audioRemoved')
          : t('detections.detail.clip.noClip')}
      </p>
    {:else if showClipHint}
      <p class="dd-rec-note">{t('detections.media.clipHint')}</p>
    {/if}
  </div>

  {#if detection.clipName}
    <a
      href={audioUrlFor(detection.id, getPlaybackDenoiseDefault())}
      download
      class="dd-rec-download"
      aria-label={t('detections.detail.aria.downloadAudioClip', { name: speciesName })}
    >
      <Download class="size-4" />
      <span>{t('media.audio.download')}</span>
    </a>
  {/if}
</header>

<style>
  .dd-rec {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.75rem;
  }

  .dd-rec-main {
    min-width: 0;
  }

  .dd-rec-title {
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-rec-fact dd {
    min-width: 0;
    font-variant-numeric: tabular-nums;
    font-weight: 500;
  }

  .dd-rec-fact-file dd {
    max-width: 22rem;
    overflow: hidden;
    font-weight: 400;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dd-rec-note {
    margin-top: 0.375rem;
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
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

  .dd-rec-download:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: 2px;
  }
</style>

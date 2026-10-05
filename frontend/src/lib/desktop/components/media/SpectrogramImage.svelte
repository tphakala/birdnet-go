<!--
  SpectrogramImage.svelte

  Plain spectrogram image for detections whose audio clip was removed by retention
  but whose spectrogram image was kept. There is no player, no download and no
  generate or status polling: the audio is gone, so nothing can change and a failed
  load is final.

  Props:
  - detectionId: Unique ID for the detection
  - size: Spectrogram size - md/lg/xl
  - raw: Add raw=true to the request; match the sibling player's value so the same
    cached file is requested (AudioPlayer omits the parameter when its raw prop is false)
  - className: Extra classes for the image container
  - alt: Alternative text for the image (default: a generic spectrogram label)
  - cover: Fill the container and crop the image to it, anchored at the bottom, instead
    of showing the image at its natural aspect ratio (used inside fixed-height cards)
-->

<script lang="ts">
  import { XCircle } from '@lucide/svelte';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { t } from '$lib/i18n';

  interface Props {
    detectionId: string;
    size: 'md' | 'lg' | 'xl';
    raw: boolean;
    className?: string;
    alt?: string;
    cover?: boolean;
  }

  let { detectionId, size, raw, className = '', alt, cover = false }: Props = $props();

  // The URL that failed to load. A different detection, size or raw flag changes the
  // URL, so it gets a fresh attempt without any reset logic.
  let failedUrl = $state<string | null>(null);

  const spectrogramUrl = $derived(
    buildAppUrl(
      `/api/v2/spectrogram/${encodeURIComponent(detectionId)}?size=${size}${raw ? '&raw=true' : ''}`
    )
  );
  const failed = $derived(failedUrl === spectrogramUrl);
</script>

<div class="spectrogram-image-container {className}" class:cover>
  {#if failed}
    <div class="spectrogram-unavailable" class:cover>
      <XCircle class="size-5 text-[var(--color-base-content)]/30" aria-hidden="true" />
      <span class="text-xs text-[var(--color-base-content)]/75">
        {t('components.audio.spectrogramUnavailable')}
      </span>
    </div>
  {:else}
    <img
      src={spectrogramUrl}
      alt={alt ?? t('components.audio.spectrogramAlt')}
      title={t('common.review.errors.noAudio')}
      loading="lazy"
      decoding="async"
      class="spectrogram-img"
      class:cover
      onerror={() => (failedUrl = spectrogramUrl)}
    />
  {/if}
</div>

<style>
  .spectrogram-image-container {
    position: relative;
    width: 100%;
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
    border-radius: 0.375rem;
    overflow: hidden;
  }

  .spectrogram-img {
    display: block;
    width: 100%;
    height: auto;
    border-radius: 0.375rem;
  }

  /* Fixed-height hosts (cards): fill the container and crop, anchored at the bottom. */
  .spectrogram-image-container.cover {
    position: absolute;
    inset: 0;
    height: 100%;
    border-radius: 0;
  }

  .spectrogram-img.cover {
    position: absolute;
    left: 0;
    bottom: 0;
    height: 100%;
    object-fit: cover;
    object-position: center bottom;
    image-rendering: pixelated;
    border-radius: 0;
  }

  .spectrogram-unavailable.cover {
    height: 100%;
    aspect-ratio: auto;
  }

  .spectrogram-unavailable {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    aspect-ratio: 2 / 1;
    width: 100%;
  }
</style>

<!--
  VideoEmbedCard - Responsive YouTube video embed for live birdfeeder cameras.
  Sanitizes YouTube URLs to use youtube-nocookie.com for privacy.
  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { VideoEmbedConfig } from '$lib/stores/settings';

  interface Props {
    config: VideoEmbedConfig;
  }

  let { config }: Props = $props();

  // Extract YouTube video ID and build safe embed URL
  let embedUrl = $derived.by(() => {
    const url = config.url?.trim();
    if (!url) return '';

    // Match various YouTube URL formats
    const patterns = [
      /(?:youtube\.com\/watch\?v=|youtu\.be\/|youtube\.com\/embed\/|youtube-nocookie\.com\/embed\/)([a-zA-Z0-9_-]{11})/,
      /^([a-zA-Z0-9_-]{11})$/, // bare video ID
    ];

    for (const pattern of patterns) {
      const match = url.match(pattern);
      if (match?.[1]) {
        return `https://www.youtube-nocookie.com/embed/${match[1]}`;
      }
    }

    // If it's already a valid embed URL, use it
    if (
      url.startsWith('https://www.youtube-nocookie.com/embed/') ||
      url.startsWith('https://www.youtube.com/embed/')
    ) {
      return url.replace('youtube.com', 'youtube-nocookie.com');
    }

    return '';
  });
</script>

{#if embedUrl}
  <div class="mb-4 overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs">
    {#if config.title}
      <div class="px-6 pt-4">
        <h3 class="text-lg font-semibold text-[var(--color-base-content)]">{config.title}</h3>
      </div>
    {/if}
    <div class="p-4">
      <div class="relative w-full" style:padding-bottom="56.25%">
        <iframe
          src={embedUrl}
          title={config.title || t('dashboard.editMode.liveBirdFeed')}
          class="absolute inset-0 h-full w-full rounded-xl"
          frameborder="0"
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
          allowfullscreen
        ></iframe>
      </div>
    </div>
  </div>
{/if}

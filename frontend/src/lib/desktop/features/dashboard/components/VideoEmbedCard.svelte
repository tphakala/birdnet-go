<!--
  VideoEmbedCard - Responsive YouTube video embed for live birdfeeder cameras.
  Sanitizes YouTube URLs to use youtube-nocookie.com for privacy.
  In edit mode, shows inline URL and title fields above the preview.
  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { VideoEmbedConfig } from '$lib/stores/settings';

  interface Props {
    config: VideoEmbedConfig;
    editMode?: boolean;
    onUpdate?: (_config: VideoEmbedConfig) => void;
  }

  let { config, editMode = false, onUpdate }: Props = $props();

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

  function update(partial: Partial<VideoEmbedConfig>) {
    onUpdate?.({ ...config, ...partial });
  }

  function inputValue(e: Event): string {
    return (e.target as HTMLInputElement).value;
  }
</script>

{#if editMode}
  <div class="h-full overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs">
    <div class="space-y-3 p-4">
      <input
        type="text"
        value={config.title}
        placeholder={t('dashboard.videoEmbed.titlePlaceholder')}
        aria-label={t('dashboard.videoEmbed.titlePlaceholder')}
        class="w-full border-0 border-b-2 border-transparent bg-transparent text-lg font-semibold text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/30 focus:border-[var(--color-primary)]/50 focus:outline-none"
        oninput={e => update({ title: inputValue(e) })}
      />
      <input
        type="text"
        value={config.url}
        placeholder={t('dashboard.videoEmbed.youtubeUrlPlaceholder')}
        aria-label={t('dashboard.videoEmbed.youtubeUrlPlaceholder')}
        class="w-full rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-100)] px-3 py-1.5 text-sm text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]/50"
        oninput={e => update({ url: inputValue(e) })}
      />
      {#if embedUrl}
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
      {:else}
        <div
          class="flex h-48 items-center justify-center rounded-xl bg-[var(--color-base-200)] text-sm text-[var(--color-base-content)]/40"
        >
          {t('dashboard.videoEmbed.youtubeUrlHelp')}
        </div>
      {/if}
    </div>
  </div>
{:else if embedUrl}
  <div class="h-full overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs">
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

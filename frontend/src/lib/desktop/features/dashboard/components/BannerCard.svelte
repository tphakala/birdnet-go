<!--
  BannerCard - Composable dashboard banner with toggleable sub-elements.
  Displays custom image, title, description, location map, and weather.
  In edit mode, provides WYSIWYG inline editing for title and description,
  plus preview of enabled sub-elements. Toggle controls are in the cogwheel settings dropdown.
  @component
-->
<script lang="ts">
  import { Cloud } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { BannerConfig } from '$lib/stores/settings';
  import BannerLocationMap from './BannerLocationMap.svelte';
  import { birdnetSettings } from '$lib/stores/settings';

  interface Props {
    config: BannerConfig;
    editMode?: boolean;
    onUpdate?: (_config: BannerConfig) => void;
  }

  let { config, editMode = false, onUpdate }: Props = $props();

  let birdnet = $derived($birdnetSettings);
  let latitude = $derived(birdnet?.latitude ?? 0);
  let longitude = $derived(birdnet?.longitude ?? 0);
  let hasLocation = $derived(latitude !== 0 || longitude !== 0);

  let hasAnyContent = $derived(
    editMode ||
      config.title ||
      config.description ||
      (config.showImage && config.imagePath) ||
      (config.showLocationMap && hasLocation) ||
      config.showWeather
  );

  function update(partial: Partial<BannerConfig>) {
    onUpdate?.({ ...config, ...partial });
  }

  function inputValue(e: Event): string {
    return (e.target as HTMLInputElement).value;
  }

  function textareaValue(e: Event): string {
    return (e.target as HTMLTextAreaElement).value;
  }
</script>

{#if hasAnyContent}
  <div
    class="flex h-full flex-col overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs"
  >
    {#if editMode}
      <!-- Edit mode: WYSIWYG vertical stacked layout -->
      <div class="flex flex-1 flex-col gap-4 p-4">
        <!-- Title (inline WYSIWYG) -->
        <input
          type="text"
          value={config.title}
          placeholder={t('dashboard.banner.titlePlaceholder')}
          aria-label={t('dashboard.banner.titlePlaceholder')}
          class="w-full border-0 border-b-2 border-transparent bg-transparent text-xl font-bold text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/30 focus:border-[var(--color-primary)]/50 focus:outline-none"
          oninput={e => update({ title: inputValue(e) })}
        />

        <!-- Description (inline WYSIWYG) -->
        <textarea
          value={config.description}
          placeholder={t('dashboard.banner.descriptionPlaceholder')}
          aria-label={t('dashboard.banner.descriptionPlaceholder')}
          class="w-full resize-y border-0 border-b-2 border-transparent bg-transparent text-sm leading-relaxed text-[var(--color-base-content)]/70 placeholder:text-[var(--color-base-content)]/30 focus:border-[var(--color-primary)]/50 focus:outline-none"
          rows="2"
          oninput={e => update({ description: textareaValue(e) })}
        ></textarea>

        <!-- Preview of enabled sub-elements -->
        {#if config.showImage && config.imagePath}
          <div class="relative">
            <img
              src={config.imagePath}
              alt={config.title || t('dashboard.editMode.stationBanner')}
              class="h-auto max-h-40 w-full rounded-xl object-cover"
            />
          </div>
        {/if}

        {#if config.showLocationMap && hasLocation}
          <BannerLocationMap
            {latitude}
            {longitude}
            zoom={config.mapZoom}
            showPin={config.showPin}
            className="h-32 rounded-xl"
          />
        {/if}

        {#if config.showWeather}
          <div class="flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
            <Cloud class="size-4" />
            <span>{t('dashboard.editMode.weatherPlaceholder')}</span>
          </div>
        {/if}
      </div>
    {:else}
      <!-- Normal mode: horizontal layout -->
      <div class="flex flex-1 p-6">
        <div class="flex flex-1 flex-col gap-6 md:flex-row">
          {#if config.showImage && config.imagePath}
            <div class="shrink-0">
              <img
                src={config.imagePath}
                alt={config.title || t('dashboard.editMode.stationBanner')}
                class="h-auto w-full rounded-xl object-cover md:w-48"
              />
            </div>
          {/if}

          <div class="min-w-0 flex-1">
            {#if config.title}
              <h2 class="mb-2 text-xl font-bold text-[var(--color-base-content)]">
                {config.title}
              </h2>
            {/if}

            {#if config.description}
              <p class="text-sm leading-relaxed text-[var(--color-base-content)]/70">
                {config.description}
              </p>
            {/if}

            {#if config.showWeather}
              <div
                class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50"
              >
                <Cloud class="size-4" />
                <span>{t('dashboard.editMode.weatherPlaceholder')}</span>
              </div>
            {/if}
          </div>

          {#if config.showLocationMap && hasLocation}
            <div class="w-full shrink-0 md:w-64">
              <BannerLocationMap
                {latitude}
                {longitude}
                zoom={config.mapZoom}
                showPin={config.showPin}
                className="h-40"
              />
            </div>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/if}

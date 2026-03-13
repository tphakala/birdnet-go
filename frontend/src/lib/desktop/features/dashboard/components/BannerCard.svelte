<!--
  BannerCard - Composable dashboard banner with toggleable sub-elements.
  Displays custom image, title, description, location map, and weather.
  @component
-->
<script lang="ts">
  import { Cloud } from '@lucide/svelte';
  import type { BannerConfig } from '$lib/stores/settings';
  import BannerLocationMap from './BannerLocationMap.svelte';
  import { birdnetSettings } from '$lib/stores/settings';

  interface Props {
    config: BannerConfig;
  }

  let { config }: Props = $props();

  let birdnet = $derived($birdnetSettings);
  let latitude = $derived(birdnet?.latitude ?? 0);
  let longitude = $derived(birdnet?.longitude ?? 0);
  let hasLocation = $derived(latitude !== 0 || longitude !== 0);

  let hasAnyContent = $derived(
    config.title ||
      config.description ||
      (config.showImage && config.imagePath) ||
      (config.showLocationMap && hasLocation) ||
      config.showWeather
  );
</script>

{#if hasAnyContent}
  <div class="mb-4 overflow-hidden rounded-2xl bg-[var(--color-base-100)] shadow-xs">
    <div class="p-6">
      <div class="flex flex-col gap-6 md:flex-row">
        <!-- Image (left side on desktop) -->
        {#if config.showImage && config.imagePath}
          <div class="shrink-0">
            <img
              src={config.imagePath}
              alt={config.title || 'Station banner'}
              class="h-auto w-full rounded-xl object-cover md:w-48"
            />
          </div>
        {/if}

        <!-- Text content -->
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

          <!-- Weather placeholder -->
          {#if config.showWeather}
            <div class="mt-3 flex items-center gap-1.5 text-sm text-[var(--color-base-content)]/50">
              <Cloud class="size-4" />
              <span>Weather conditions will appear here when available</span>
            </div>
          {/if}
        </div>

        <!-- Location map (right side on desktop) -->
        {#if config.showLocationMap && hasLocation}
          <div class="w-full shrink-0 md:w-64">
            <BannerLocationMap {latitude} {longitude} className="h-40" />
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}

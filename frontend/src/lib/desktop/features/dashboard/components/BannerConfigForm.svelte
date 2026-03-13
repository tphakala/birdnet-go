<!--
  Configuration form for the Banner dashboard element.
  @component
-->
<script lang="ts">
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import { t } from '$lib/i18n';
  import type { BannerConfig } from '$lib/stores/settings';

  interface Props {
    config: BannerConfig;
    onUpdate: (_config: BannerConfig) => void;
  }

  let { config, onUpdate }: Props = $props();

  function update(partial: Partial<BannerConfig>) {
    onUpdate({ ...config, ...partial });
  }
</script>

<div class="space-y-4">
  <TextInput
    id="banner-title"
    value={config.title}
    label={t('dashboard.banner.title')}
    placeholder={t('dashboard.banner.titlePlaceholder')}
    onchange={value => update({ title: value })}
  />

  <div>
    <label
      for="banner-description"
      class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
    >
      {t('dashboard.banner.description')}
    </label>
    <textarea
      id="banner-description"
      value={config.description}
      placeholder={t('dashboard.banner.descriptionPlaceholder')}
      class="w-full resize-y rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-100)] px-3 py-2 text-sm text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]/50"
      rows="3"
      oninput={e => update({ description: (e.target as HTMLTextAreaElement).value })}
    ></textarea>
  </div>

  <div class="space-y-3 border-t border-[var(--color-base-200)] pt-4">
    <h4 class="text-sm font-medium text-[var(--color-base-content)]/70">
      {t('dashboard.banner.subElements')}
    </h4>

    <Checkbox
      checked={config.showImage}
      label={t('dashboard.banner.showImage')}
      onchange={value => update({ showImage: value })}
    />

    {#if config.showImage}
      <TextInput
        id="banner-image"
        value={config.imagePath}
        label={t('dashboard.banner.imageUrl')}
        placeholder={t('dashboard.banner.imageUrlPlaceholder')}
        helpText={t('dashboard.banner.imageUrlHelp')}
        onchange={value => update({ imagePath: value })}
      />
    {/if}

    <Checkbox
      checked={config.showLocationMap}
      label={t('dashboard.banner.showLocationMap')}
      helpText={t('dashboard.banner.showLocationMapHelp')}
      onchange={value => update({ showLocationMap: value })}
    />

    <Checkbox
      checked={config.showWeather}
      label={t('dashboard.banner.showWeather')}
      helpText={t('dashboard.banner.showWeatherHelp')}
      onchange={value => update({ showWeather: value })}
    />
  </div>
</div>

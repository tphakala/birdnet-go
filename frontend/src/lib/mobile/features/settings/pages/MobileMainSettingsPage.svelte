<script lang="ts">
  import { t } from '$lib/i18n';
  import {
    settingsStore,
    settingsActions,
    mainSettings,
    birdnetSettings,
  } from '$lib/stores/settings';
  import {
    MobileTextInput,
    MobileNumberInput,
    MobileSlider,
    MobileSelect,
  } from '../../../components/forms';

  let store = $derived($settingsStore);

  let settings = $derived({
    main: $mainSettings ?? { name: '' },
    birdnet: $birdnetSettings ?? {
      sensitivity: 1.0,
      threshold: 0.8,
      overlap: 0.0,
      locale: 'en',
      threads: 0,
    },
  });

  const localeOptions = [
    { value: 'en', label: 'English' },
    { value: 'de', label: 'Deutsch' },
    { value: 'es', label: 'Español' },
    { value: 'fr', label: 'Français' },
  ];

  function updateNodeName(name: string) {
    settingsActions.updateSection('main', { name });
  }

  function updateBirdnetSetting(key: string, value: number | string) {
    settingsActions.updateSection('birdnet', { [key]: value });
  }

  async function handleSave() {
    await settingsActions.saveSettings();
  }
</script>

<div class="flex flex-col gap-6 p-4">
  <!-- Node Settings -->
  <div class="space-y-4">
    <h2 class="text-lg font-semibold">{t('settings.main.node.title')}</h2>
    <MobileTextInput
      label={t('settings.main.node.name')}
      value={settings.main.name}
      helpText={t('settings.main.node.nameHelp')}
      onchange={updateNodeName}
    />
  </div>

  <!-- BirdNET Settings -->
  <div class="space-y-4">
    <h2 class="text-lg font-semibold">{t('settings.main.birdnet.title')}</h2>

    <MobileSlider
      label={t('settings.main.birdnet.sensitivity')}
      value={settings.birdnet.sensitivity}
      min={0}
      max={1.5}
      step={0.1}
      helpText={t('settings.main.birdnet.sensitivityHelp')}
      onUpdate={v => updateBirdnetSetting('sensitivity', v)}
    />

    <MobileSlider
      label={t('settings.main.birdnet.threshold')}
      value={settings.birdnet.threshold}
      min={0}
      max={1}
      step={0.05}
      helpText={t('settings.main.birdnet.thresholdHelp')}
      onUpdate={v => updateBirdnetSetting('threshold', v)}
    />

    <MobileSlider
      label={t('settings.main.birdnet.overlap')}
      value={settings.birdnet.overlap}
      min={0}
      max={2.9}
      step={0.1}
      suffix="s"
      helpText={t('settings.main.birdnet.overlapHelp')}
      onUpdate={v => updateBirdnetSetting('overlap', v)}
    />

    <MobileSelect
      label={t('settings.main.birdnet.locale')}
      value={settings.birdnet.locale}
      options={localeOptions}
      helpText={t('settings.main.birdnet.localeHelp')}
      onchange={v => updateBirdnetSetting('locale', v)}
    />

    <MobileNumberInput
      label={t('settings.main.birdnet.threads')}
      value={settings.birdnet.threads}
      min={0}
      max={16}
      step={1}
      helpText={t('settings.main.birdnet.threadsHelp')}
      onUpdate={v => updateBirdnetSetting('threads', v)}
    />
  </div>

  <!-- Save Button -->
  <div class="pt-4">
    <button
      class="btn btn-primary w-full h-12"
      onclick={handleSave}
      disabled={store.isLoading || store.isSaving}
    >
      {#if store.isSaving}
        <span class="loading loading-spinner loading-sm"></span>
      {/if}
      {t('settings.save')}
    </button>
  </div>
</div>

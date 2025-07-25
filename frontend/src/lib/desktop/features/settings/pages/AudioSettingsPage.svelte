<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import RTSPUrlInput from '$lib/desktop/components/forms/RTSPUrlInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import {
    settingsStore,
    settingsActions,
    audioSettings,
    rtspSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { RTSPUrl } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';

  // Localized option arrays - memoized to prevent unnecessary recomputations
  // These will only recompute when the locale changes, not on every reactive update
  import { getLocale } from '$lib/i18n';

  const exportFormatOptions = $derived.by(() => {
    // By accessing getLocale(), this will only recompute when locale changes
    getLocale();
    return [
      { value: 'wav', label: t('settings.audio.formats.wav') },
      { value: 'flac', label: t('settings.audio.formats.flac') },
      { value: 'aac', label: t('settings.audio.formats.aac') },
      { value: 'opus', label: t('settings.audio.formats.opus') },
      { value: 'mp3', label: t('settings.audio.formats.mp3') },
    ];
  });

  const retentionPolicyOptions = $derived.by(() => {
    // By accessing getLocale(), this will only recompute when locale changes
    getLocale();
    return [
      { value: 'none', label: t('settings.audio.audioClipRetention.policies.none') },
      { value: 'age', label: t('settings.audio.audioClipRetention.policies.age') },
      { value: 'usage', label: t('settings.audio.audioClipRetention.policies.usage') },
    ];
  });

  let settings = $derived({
    audio: $audioSettings || {
      source: '',
      soundLevel: {
        enabled: false,
        interval: 60,
      },
      equalizer: {
        enabled: false,
        filters: [],
      },
      export: {
        enabled: false,
        debug: false,
        path: 'clips/',
        type: 'wav' as const,
        bitrate: '96k',
      },
      retention: {
        policy: 'none',
        maxAge: '7d',
        maxUsage: '80%',
        minClips: 10,
        keepSpectrograms: false,
      },
    },
    rtsp: $rtspSettings || {
      transport: 'tcp' as const,
      urls: [] as RTSPUrl[],
    },
  });
  let store = $derived($settingsStore);

  // Check for changes in audio settings sections (only core capture settings)
  let audioCaptureHasChanges = $derived(
    hasSettingsChanged(
      {
        source: (store.originalData as any)?.realtime?.audio?.source,
        rtsp: (store.originalData as any)?.realtime?.rtsp,
      },
      {
        source: (store.formData as any)?.realtime?.audio?.source,
        rtsp: (store.formData as any)?.realtime?.rtsp,
      }
    )
  );

  let audioExportHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.audio?.export,
      (store.formData as any)?.realtime?.audio?.export
    )
  );

  let audioRetentionHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.audio?.retention,
      (store.formData as any)?.realtime?.audio?.retention
    )
  );

  let audioFiltersHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.audio?.equalizer,
      (store.formData as any)?.realtime?.audio?.equalizer
    )
  );

  let soundLevelHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.audio?.soundLevel,
      (store.formData as any)?.realtime?.audio?.soundLevel
    )
  );

  // Audio source options - map to actual device names
  let audioDevices = $state<Array<{ Index: number; Name: string }>>([]);

  // Equalizer filter configuration (static from backend)
  const eqFilterConfig: Record<
    string,
    {
      Parameters: Array<{
        Name: string;
        Label: string;
        Type: string;
        Unit?: string;
        Min: number;
        Max: number;
        Default: number;
      }>;
    }
  > = {
    lowpass: {
      Parameters: [
        {
          Name: 'frequency',
          Label: 'Cutoff Frequency',
          Type: 'number',
          Unit: 'Hz',
          Min: 20,
          Max: 20000,
          Default: 15000,
        },
        { Name: 'q', Label: 'Q Factor', Type: 'number', Min: 0.1, Max: 10, Default: 0.707 },
      ],
    },
    highpass: {
      Parameters: [
        {
          Name: 'frequency',
          Label: 'Cutoff Frequency',
          Type: 'number',
          Unit: 'Hz',
          Min: 20,
          Max: 20000,
          Default: 100,
        },
        { Name: 'q', Label: 'Q Factor', Type: 'number', Min: 0.1, Max: 10, Default: 0.707 },
      ],
    },
  };

  // Helper function to get translated parameter label
  function getParameterLabel(paramName: string): string {
    const labelMap: Record<string, string> = {
      frequency: t('settings.audio.audioFilters.cutoffFrequency'),
      q: t('settings.audio.audioFilters.qFactor'),
      gain: t('settings.audio.audioFilters.gain'),
      attenuation: t('settings.audio.audioFilters.attenuation'),
    };
    return labelMap[paramName.toLowerCase()] || paramName;
  }

  // New filter state for adding filters
  let newFilter = $state({
    id: '',
    type: '' as 'highpass' | 'lowpass' | 'bandpass' | 'bandstop' | '',
    frequency: 0,
    q: 0,
    gain: 0,
  });

  // Fetch audio devices on mount
  $effect(() => {
    fetch('/api/v1/settings/audio/get')
      .then(response => response.json())
      .then(data => {
        audioDevices = data || [];
      })
      .catch(error => console.error('Error fetching audio devices:', error));
  });

  // Check if ffmpeg is available
  let ffmpegAvailable = $state(true); // Assume true for now

  // Retention settings with proper structure
  let retentionSettings = $derived({
    policy: settings.audio.retention?.policy || 'none',
    maxAge: settings.audio.retention?.maxAge || '7d',
    maxUsage: settings.audio.retention?.maxUsage || '80%',
    minClips: settings.audio.retention?.minClips || 10,
    keepSpectrograms: settings.audio.retention?.keepSpectrograms || false,
  });

  // Update handlers
  function updateAudioSource(source: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, source },
    });
  }

  function updateRTSPTransport(transport: 'tcp' | 'udp') {
    settingsActions.updateSection('realtime', {
      rtsp: { ...settings.rtsp, transport },
    });
  }

  function updateRTSPUrls(urls: RTSPUrl[]) {
    settingsActions.updateSection('realtime', {
      rtsp: { ...settings.rtsp, urls },
    });
  }

  function updateExportEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, export: { ...settings.audio.export, enabled } },
    });
  }

  function updateExportFormat(type: 'wav' | 'mp3' | 'flac' | 'aac' | 'opus') {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, export: { ...settings.audio.export, type } },
    });
  }

  function updateExportBitrate(bitrate: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, export: { ...settings.audio.export, bitrate } },
    });
  }

  // Update retention settings
  function updateRetentionPolicy(policy: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, retention: { ...retentionSettings, policy } },
    });
  }

  function updateRetentionMaxAge(maxAge: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, retention: { ...retentionSettings, maxAge } },
    });
  }

  function updateRetentionMaxUsage(maxUsage: string) {
    // Ensure it has % suffix
    if (!maxUsage.endsWith('%') && !isNaN(Number(maxUsage))) {
      maxUsage = maxUsage + '%';
    }
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, retention: { ...retentionSettings, maxUsage } },
    });
  }

  function updateRetentionMinClips(minClips: number) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, retention: { ...retentionSettings, minClips } },
    });
  }

  function updateRetentionKeepSpectrograms(keepSpectrograms: boolean) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, retention: { ...retentionSettings, keepSpectrograms } },
    });
  }

  // Equalizer functions
  function getEqFilterParameters(filterType: string) {
    return eqFilterConfig[filterType]?.Parameters || [];
  }

  function addNewFilter() {
    if (!newFilter.type) return;

    const filterWithId = {
      ...newFilter,
      id: `filter-${Date.now()}`,
      type: newFilter.type as 'highpass' | 'lowpass' | 'bandpass' | 'bandstop',
    };

    const filters = [...(settings.audio.equalizer.filters || []), filterWithId];
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        equalizer: { ...settings.audio.equalizer, filters },
      },
    });

    // Reset new filter form
    newFilter = { id: '', type: '', frequency: 0, q: 0, gain: 0 };
  }

  function removeFilter(index: number) {
    const filters = settings.audio.equalizer.filters.filter((_, i) => i !== index);
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        equalizer: { ...settings.audio.equalizer, filters },
      },
    });
  }

  function updateFilterParameter(index: number, paramName: string, value: any) {
    const filters = [...settings.audio.equalizer.filters];
    filters[index] = { ...filters[index], [paramName.toLowerCase()]: value };

    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        equalizer: { ...settings.audio.equalizer, filters },
      },
    });
  }

  function getFilterDefaults(filterType: string) {
    if (!filterType) {
      newFilter = { id: '', type: '', frequency: 0, q: 0, gain: 0 };
      return;
    }

    const parameters = getEqFilterParameters(filterType);
    const updatedFilter = {
      id: '',
      type: filterType as 'highpass' | 'lowpass' | 'bandpass' | 'bandstop',
      frequency: 0,
      q: 0,
      gain: 0,
    };

    parameters.forEach(param => {
      const paramName = param.Name.toLowerCase() as keyof typeof updatedFilter;
      if (paramName in updatedFilter && typeof updatedFilter[paramName] === 'number') {
        (updatedFilter as any)[paramName] = param.Default;
      }
    });

    newFilter = updatedFilter;
  }
</script>

{#if store.isLoading}
  <div class="flex items-center justify-center py-12">
    <div class="loading loading-spinner loading-lg"></div>
  </div>
{:else}
  <div class="space-y-4">
    <!-- Audio Capture Section -->
    <SettingsSection
      title={t('settings.audio.audioCapture.title')}
      description={t('settings.audio.audioCapture.description')}
      defaultOpen={true}
      hasChanges={audioCaptureHasChanges}
    >
      <div class="space-y-6">
        <!-- Sound Card Source -->
        <div>
          <h4 class="text-lg font-medium pb-2">
            {t('settings.audio.audioCapture.soundCardSource')}
          </h4>
          <SelectField
            id="audio-source"
            bind:value={settings.audio.source}
            label={t('settings.audio.audioCapture.audioSourceLabel')}
            placeholder={t('settings.audio.audioCapture.noSoundCardCapture')}
            disabled={store.isLoading || store.isSaving}
            onchange={updateAudioSource}
            options={[]}
          >
            <option value="">{t('settings.audio.audioCapture.noSoundCardCapture')}</option>
            {#each audioDevices as device}
              <option value={device.Name}>{device.Name}</option>
            {/each}
          </SelectField>
        </div>

        <!-- RTSP Source -->
        <div>
          <h4 class="text-lg font-medium pt-4 pb-2">
            {t('settings.audio.audioCapture.rtspSource')}
          </h4>

          <!-- Transport Protocol -->
          <div class="mb-4">
            <SelectField
              id="rtsp-transport"
              bind:value={settings.rtsp.transport}
              label={t('settings.audio.audioCapture.rtspTransportLabel')}
              options={[
                { value: 'tcp', label: t('settings.audio.transport.tcp') },
                { value: 'udp', label: t('settings.audio.transport.udp') },
              ]}
              disabled={store.isLoading || store.isSaving}
              onchange={value => updateRTSPTransport(value as 'tcp' | 'udp')}
            />
          </div>

          <!-- RTSP URLs -->
          <div class="form-control">
            <label class="label" for="rtsp-urls">
              <span class="label-text">{t('settings.audio.audioCapture.rtspUrlsLabel')}</span>
            </label>
            <div id="rtsp-urls">
              <RTSPUrlInput
                urls={settings.rtsp.urls}
                onUpdate={updateRTSPUrls}
                disabled={store.isLoading || store.isSaving}
              />
            </div>
          </div>
        </div>
      </div>
    </SettingsSection>

    <!-- Audio Filters Section -->
    <SettingsSection
      title={t('settings.audio.audioFilters.title')}
      description={t('settings.audio.audioFilters.description')}
      defaultOpen={false}
      hasChanges={audioFiltersHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.equalizer.enabled}
          label={t('settings.audio.audioFilters.enableEqualizer')}
          disabled={store.isLoading || store.isSaving}
          onchange={() =>
            settingsActions.updateSection('realtime', {
              audio: {
                ...$audioSettings!,
                equalizer: {
                  ...settings.audio.equalizer,
                  enabled: settings.audio.equalizer.enabled,
                },
              },
            })}
        />

        {#if settings.audio.equalizer.enabled}
          <div class="space-y-4">
            <!-- Existing filters -->
            {#each settings.audio.equalizer.filters || [] as filter, index}
              <div class="border border-base-300 rounded-lg p-4 bg-base-200">
                <div class="grid grid-cols-1 md:grid-cols-5 gap-4 items-end">
                  <!-- Filter Type -->
                  <div class="flex flex-col">
                    <div class="label">
                      <span class="label-text">{t('settings.audio.audioFilters.filterType')}</span>
                    </div>
                    <div class="btn btn-sm w-full pointer-events-none bg-base-300 border-base-300">
                      {t(`settings.audio.filterTypes.${filter.type}`)}
                    </div>
                  </div>

                  <!-- Dynamic parameters based on filter type -->
                  {#each getEqFilterParameters(filter.type) as param}
                    <div class="flex flex-col">
                      {#if param.Name.toLowerCase() === 'attenuation'}
                        <!-- Special handling for Attenuation (Passes) -->
                        <SelectField
                          id="filter-{index}-{param.Name}"
                          value={(filter as any)[param.Name.toLowerCase()]}
                          onchange={value =>
                            updateFilterParameter(index, param.Name, parseInt(value))}
                          options={[
                            {
                              value: '0',
                              label: t('settings.audio.audioFilters.attenuationLevels.0db'),
                            },
                            {
                              value: '1',
                              label: t('settings.audio.audioFilters.attenuationLevels.12db'),
                            },
                            {
                              value: '2',
                              label: t('settings.audio.audioFilters.attenuationLevels.24db'),
                            },
                            {
                              value: '3',
                              label: t('settings.audio.audioFilters.attenuationLevels.36db'),
                            },
                            {
                              value: '4',
                              label: t('settings.audio.audioFilters.attenuationLevels.48db'),
                            },
                          ]}
                          className="select-sm"
                          disabled={store.isLoading || store.isSaving}
                          label="{getParameterLabel(param.Name)}{param.Unit
                            ? ` (${param.Unit})`
                            : ''}"
                        />
                      {:else}
                        <!-- Regular number input -->
                        <NumberField
                          value={(filter as any)[param.Name.toLowerCase()] || param.Default}
                          onUpdate={value => updateFilterParameter(index, param.Name, value)}
                          min={param.Min}
                          max={param.Max}
                          step={param.Type === 'float' ? 0.1 : 1}
                          disabled={store.isLoading || store.isSaving}
                          label="{getParameterLabel(param.Name)}{param.Unit
                            ? ` (${param.Unit})`
                            : ''}"
                        />
                      {/if}
                    </div>
                  {/each}

                  <!-- Remove button -->
                  <div class="flex flex-col items-end">
                    <div class="label">
                      <span class="label-text">&nbsp;</span>
                    </div>
                    <button
                      type="button"
                      class="btn btn-error btn-sm w-full md:w-24"
                      onclick={() => removeFilter(index)}
                      disabled={store.isLoading || store.isSaving}
                    >
                      {t('settings.audio.audioFilters.remove')}
                    </button>
                  </div>
                </div>
              </div>
            {/each}

            <!-- Add new filter -->
            <div class="border border-dashed border-base-300 rounded-lg p-4">
              <div class="grid grid-cols-1 md:grid-cols-5 gap-4 items-end">
                <!-- New Filter Type -->
                <div class="flex flex-col">
                  <SelectField
                    id="new-filter-type"
                    value={newFilter.type}
                    onchange={value => {
                      newFilter.type = value as
                        | 'highpass'
                        | 'lowpass'
                        | 'bandpass'
                        | 'bandstop'
                        | '';
                      getFilterDefaults(value);
                    }}
                    options={[]}
                    placeholder={t('settings.audio.audioFilters.selectFilterType')}
                    className="select-sm"
                    disabled={store.isLoading || store.isSaving}
                    label={t('settings.audio.audioFilters.newFilterType')}
                  >
                    <option value="">{t('settings.audio.audioFilters.selectFilterType')}</option>
                    {#each Object.keys(eqFilterConfig) as filterType}
                      <option value={filterType}
                        >{t(`settings.audio.filterTypes.${filterType}`)}</option
                      >
                    {/each}
                  </SelectField>
                </div>

                <!-- Dynamic parameters for new filter -->
                {#if newFilter.type}
                  {#each getEqFilterParameters(newFilter.type) as param}
                    <div class="flex flex-col">
                      {#if param.Name.toLowerCase() === 'attenuation'}
                        <!-- Special handling for Attenuation -->
                        <SelectField
                          id="new-filter-{param.Name}"
                          value={(newFilter as any)[param.Name.toLowerCase()]}
                          onchange={value => {
                            (newFilter as any)[param.Name.toLowerCase()] = parseInt(value);
                          }}
                          options={[
                            {
                              value: '0',
                              label: t('settings.audio.audioFilters.attenuationLevels.0db'),
                            },
                            {
                              value: '1',
                              label: t('settings.audio.audioFilters.attenuationLevels.12db'),
                            },
                            {
                              value: '2',
                              label: t('settings.audio.audioFilters.attenuationLevels.24db'),
                            },
                            {
                              value: '3',
                              label: t('settings.audio.audioFilters.attenuationLevels.36db'),
                            },
                            {
                              value: '4',
                              label: t('settings.audio.audioFilters.attenuationLevels.48db'),
                            },
                          ]}
                          className="select-sm"
                          disabled={store.isLoading || store.isSaving}
                          label="{getParameterLabel(param.Name)}{param.Unit
                            ? ` (${param.Unit})`
                            : ''}"
                        />
                      {:else}
                        <!-- Regular number input -->
                        <NumberField
                          value={(newFilter as any)[param.Name.toLowerCase()]}
                          onUpdate={value => {
                            (newFilter as any)[param.Name.toLowerCase()] = value;
                          }}
                          min={param.Min}
                          max={param.Max}
                          step={param.Type === 'float' ? 0.1 : 1}
                          disabled={store.isLoading || store.isSaving}
                          label="{getParameterLabel(param.Name)}{param.Unit
                            ? ` (${param.Unit})`
                            : ''}"
                        />
                      {/if}
                    </div>
                  {/each}
                {/if}

                <!-- Add button -->
                <div class="flex flex-col">
                  <div class="label">
                    <span class="label-text">&nbsp;</span>
                  </div>
                  <button
                    type="button"
                    class="btn btn-primary btn-sm w-24"
                    onclick={addNewFilter}
                    disabled={!newFilter.type || store.isLoading || store.isSaving}
                  >
                    {t('settings.audio.audioFilters.addFilter')}
                  </button>
                </div>
              </div>
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Sound Level Monitoring Section -->
    <SettingsSection
      title={t('settings.audio.soundLevelMonitoring.title')}
      description={t('settings.audio.soundLevelMonitoring.description')}
      defaultOpen={false}
      hasChanges={soundLevelHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.soundLevel.enabled}
          label={t('settings.audio.soundLevelMonitoring.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() =>
            settingsActions.updateSection('realtime', {
              audio: {
                ...$audioSettings!,
                soundLevel: {
                  ...settings.audio.soundLevel,
                  enabled: settings.audio.soundLevel.enabled,
                },
              },
            })}
        />

        {#if settings.audio.soundLevel.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-4">
            <NumberField
              label={t('settings.audio.soundLevelMonitoring.intervalLabel')}
              value={settings.audio.soundLevel.interval}
              onUpdate={value =>
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    soundLevel: { ...settings.audio.soundLevel, interval: value },
                  },
                })}
              min={5}
              max={300}
              step={1}
              placeholder="60"
              helpText={t('settings.audio.soundLevelMonitoring.intervalHelp')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <SettingsNote>
            {#snippet icon()}
              {@html alertIconsSvg.info}
            {/snippet}
            <p class="font-semibold">
              {t('settings.audio.soundLevelMonitoring.dataOutputTitle')}
            </p>
            <p class="text-sm">
              {t('settings.audio.soundLevelMonitoring.dataOutputDescription')}
            </p>
            <ul class="text-sm list-disc list-inside mt-1">
              <li>
                {t('settings.audio.soundLevelMonitoring.mqttTopic')}
                <code>{'{base_topic}'}/soundlevel</code>
              </li>
              <li>
                {t('settings.audio.soundLevelMonitoring.sseEndpoint')}
                <code>/api/v2/soundlevels/stream</code>
              </li>
              <li>
                {t('settings.audio.soundLevelMonitoring.prometheusMetrics')}
                <code>birdnet_sound_level_db</code>
              </li>
            </ul>
          </SettingsNote>
        {/if}
      </div>
    </SettingsSection>

    <!-- Audio Export Section -->
    <SettingsSection
      title={t('settings.audio.audioExport.title')}
      description={t('settings.audio.audioExport.description')}
      defaultOpen={true}
      hasChanges={audioExportHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.export.enabled}
          label={t('settings.audio.audioExport.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateExportEnabled(settings.audio.export.enabled)}
        />

        {#if settings.audio.export.enabled}
          <Checkbox
            bind:checked={settings.audio.export.debug}
            label={t('settings.audio.audioExport.enableDebug')}
            disabled={store.isLoading || store.isSaving}
            onchange={() =>
              settingsActions.updateSection('realtime', {
                audio: {
                  ...$audioSettings!,
                  export: { ...settings.audio.export, debug: settings.audio.export.debug },
                },
              })}
          />

          <div class="grid grid-cols-1 md:grid-cols-3 gap-6">
            <!-- Export Path -->
            <TextInput
              id="export-path"
              bind:value={settings.audio.export.path}
              label={t('settings.audio.audioExport.pathLabel')}
              placeholder="clips/"
              disabled={store.isLoading || store.isSaving}
              onchange={value =>
                settingsActions.updateSection('realtime', {
                  audio: { ...$audioSettings!, export: { ...settings.audio.export, path: value } },
                })}
            />

            <!-- Export Type -->
            <SelectField
              id="export-type"
              bind:value={settings.audio.export.type}
              label={t('settings.audio.audioExport.typeLabel')}
              options={exportFormatOptions}
              disabled={store.isLoading || store.isSaving}
              onchange={value =>
                updateExportFormat(value as 'wav' | 'mp3' | 'flac' | 'aac' | 'opus')}
            />

            <!-- Bitrate -->
            <TextInput
              id="export-bitrate"
              bind:value={settings.audio.export.bitrate}
              label={t('settings.audio.audioExport.bitrateLabel')}
              placeholder="96k"
              disabled={store.isLoading ||
                store.isSaving ||
                !ffmpegAvailable ||
                !['aac', 'opus', 'mp3'].includes(settings.audio.export.type)}
              onchange={updateExportBitrate}
            />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Audio Clip Retention Section -->
    <SettingsSection
      title={t('settings.audio.audioClipRetention.title')}
      description={t('settings.audio.audioClipRetention.description')}
      defaultOpen={true}
      hasChanges={audioRetentionHasChanges}
    >
      <div class="space-y-4">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <!-- Retention Policy -->
          <SelectField
            id="retention-policy"
            bind:value={retentionSettings.policy}
            label={t('settings.audio.audioClipRetention.policyLabel')}
            options={retentionPolicyOptions}
            disabled={store.isLoading || store.isSaving}
            onchange={updateRetentionPolicy}
          />

          <!-- Max Age (shown when policy is 'age') -->
          {#if retentionSettings.policy === 'age'}
            <TextInput
              id="retention-max-age"
              bind:value={retentionSettings.maxAge}
              label={t('settings.audio.audioClipRetention.maxAgeLabel')}
              placeholder="7d"
              disabled={store.isLoading || store.isSaving}
              onchange={updateRetentionMaxAge}
            />
          {/if}

          <!-- Max Usage (shown when policy is 'usage') -->
          {#if retentionSettings.policy === 'usage'}
            <TextInput
              id="retention-max-usage"
              bind:value={retentionSettings.maxUsage}
              label={t('settings.audio.audioClipRetention.maxUsageLabel')}
              placeholder="80%"
              disabled={store.isLoading || store.isSaving}
              oninput={updateRetentionMaxUsage}
            />
          {/if}
        </div>

        {#if retentionSettings.policy !== 'none'}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <!-- Minimum Clips -->
            <NumberField
              label={t('settings.audio.audioClipRetention.minClipsLabel')}
              value={retentionSettings.minClips}
              onUpdate={updateRetentionMinClips}
              min={0}
              placeholder="10"
              disabled={store.isLoading || store.isSaving}
            />

            <!-- Keep Spectrograms -->
            <div class="mt-8">
              <Checkbox
                bind:checked={retentionSettings.keepSpectrograms}
                label={t('settings.audio.audioClipRetention.keepSpectrograms')}
                disabled={store.isLoading || store.isSaving}
                onchange={() => updateRetentionKeepSpectrograms(retentionSettings.keepSpectrograms)}
              />
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/if}

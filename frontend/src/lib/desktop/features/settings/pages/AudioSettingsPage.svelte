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
  import { alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts

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

  // Export format options
  const exportFormatOptions = [
    { value: 'wav', label: 'WAV' },
    { value: 'flac', label: 'FLAC' },
    { value: 'aac', label: 'AAC' },
    { value: 'opus', label: 'Opus' },
    { value: 'mp3', label: 'MP3' },
  ];

  // Retention policy options
  const retentionPolicyOptions = [
    { value: 'none', label: 'None' },
    { value: 'age', label: 'Age' },
    { value: 'usage', label: 'Usage' },
  ];

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
      title="Audio Capture"
      description="Set audio capture source, sound card or RTSP stream"
      defaultOpen={true}
      hasChanges={audioCaptureHasChanges}
    >
      <div class="space-y-6">
        <!-- Sound Card Source -->
        <div>
          <h4 class="text-lg font-medium pb-2">Sound Card Source</h4>
          <SelectField
            id="audio-source"
            bind:value={settings.audio.source}
            label="Audio Source (requires application restart to take effect)"
            placeholder="No sound card capture"
            disabled={store.isLoading || store.isSaving}
            onchange={updateAudioSource}
            options={[]}
          >
            <option value="">No sound card capture</option>
            {#each audioDevices as device}
              <option value={device.Name}>{device.Name}</option>
            {/each}
          </SelectField>
        </div>

        <!-- RTSP Source -->
        <div>
          <h4 class="text-lg font-medium pt-4 pb-2">RTSP Source</h4>

          <!-- Transport Protocol -->
          <div class="mb-4">
            <SelectField
              id="rtsp-transport"
              bind:value={settings.rtsp.transport}
              label="RTSP Transport Protocol"
              options={[
                { value: 'tcp', label: 'TCP' },
                { value: 'udp', label: 'UDP' },
              ]}
              disabled={store.isLoading || store.isSaving}
              onchange={value => updateRTSPTransport(value as 'tcp' | 'udp')}
            />
          </div>

          <!-- RTSP URLs -->
          <div class="form-control">
            <label class="label" for="rtsp-urls">
              <span class="label-text">RTSP Stream URLs</span>
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
      title="Audio Filters"
      description="Configure audio processing filters"
      defaultOpen={false}
      hasChanges={audioFiltersHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.equalizer.enabled}
          label="Enable Audio Equalizer"
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
                      <span class="label-text">Filter Type</span>
                    </div>
                    <div class="btn btn-sm w-full pointer-events-none bg-base-300 border-base-300">
                      {filter.type} Filter
                    </div>
                  </div>

                  <!-- Dynamic parameters based on filter type -->
                  {#each getEqFilterParameters(filter.type) as param}
                    <div class="flex flex-col">
                      {#if param.Label === 'Attenuation'}
                        <!-- Special handling for Attenuation (Passes) -->
                        <SelectField
                          id="filter-{index}-{param.Name}"
                          value={(filter as any)[param.Name.toLowerCase()]}
                          onchange={value =>
                            updateFilterParameter(index, param.Name, parseInt(value))}
                          options={[
                            { value: '0', label: '0dB' },
                            { value: '1', label: '12dB' },
                            { value: '2', label: '24dB' },
                            { value: '3', label: '36dB' },
                            { value: '4', label: '48dB' },
                          ]}
                          className="select-sm"
                          disabled={store.isLoading || store.isSaving}
                          label="{param.Label}{param.Unit ? ` (${param.Unit})` : ''}"
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
                          label="{param.Label}{param.Unit ? ` (${param.Unit})` : ''}"
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
                      Remove
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
                    placeholder="Select filter type"
                    className="select-sm"
                    disabled={store.isLoading || store.isSaving}
                    label="New Filter Type"
                  >
                    <option value="">Select filter type</option>
                    {#each Object.keys(eqFilterConfig) as filterType}
                      <option value={filterType}>{filterType}</option>
                    {/each}
                  </SelectField>
                </div>

                <!-- Dynamic parameters for new filter -->
                {#if newFilter.type}
                  {#each getEqFilterParameters(newFilter.type) as param}
                    <div class="flex flex-col">
                      {#if param.Label === 'Attenuation'}
                        <!-- Special handling for Attenuation -->
                        <SelectField
                          id="new-filter-{param.Name}"
                          value={(newFilter as any)[param.Name.toLowerCase()]}
                          onchange={value => {
                            (newFilter as any)[param.Name.toLowerCase()] = parseInt(value);
                          }}
                          options={[
                            { value: '0', label: '0dB' },
                            { value: '1', label: '12dB' },
                            { value: '2', label: '24dB' },
                            { value: '3', label: '36dB' },
                            { value: '4', label: '48dB' },
                          ]}
                          className="select-sm"
                          disabled={store.isLoading || store.isSaving}
                          label="{param.Label}{param.Unit ? ` (${param.Unit})` : ''}"
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
                          label="{param.Label}{param.Unit ? ` (${param.Unit})` : ''}"
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
                    Add Filter
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
      title="Sound Level Monitoring"
      description="Monitor and report environmental sound levels"
      defaultOpen={false}
      hasChanges={soundLevelHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.soundLevel.enabled}
          label="Enable Sound Level Monitoring"
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
              label="Measurement Interval (seconds)"
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
              helpText="How often to report sound level measurements"
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <div class="alert alert-info mt-4">
            {@html alertIconsSvg.info}
            <div>
              <p class="font-semibold">Sound Level Data Output</p>
              <p class="text-sm">When enabled, sound level measurements are published via:</p>
              <ul class="text-sm list-disc list-inside mt-1">
                <li>MQTT topic: <code>{'{base_topic}'}/soundlevel</code></li>
                <li>SSE endpoint: <code>/api/v2/soundlevels/stream</code></li>
                <li>Prometheus metrics: <code>birdnet_sound_level_db</code></li>
              </ul>
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Audio Export Section -->
    <SettingsSection
      title="Audio Export"
      description="Configure audio clip saving for identified bird calls"
      defaultOpen={true}
      hasChanges={audioExportHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.audio.export.enabled}
          label="Enable Audio Export"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateExportEnabled(settings.audio.export.enabled)}
        />

        {#if settings.audio.export.enabled}
          <Checkbox
            bind:checked={settings.audio.export.debug}
            label="Enable Debug Mode"
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
              label="Export Path"
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
              label="Export Type"
              options={exportFormatOptions}
              disabled={store.isLoading || store.isSaving}
              onchange={value =>
                updateExportFormat(value as 'wav' | 'mp3' | 'flac' | 'aac' | 'opus')}
            />

            <!-- Bitrate -->
            <TextInput
              id="export-bitrate"
              bind:value={settings.audio.export.bitrate}
              label="Bitrate"
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
      title="Audio Clip Retention"
      description="Configure audio clip cleanup"
      defaultOpen={true}
      hasChanges={audioRetentionHasChanges}
    >
      <div class="space-y-4">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <!-- Retention Policy -->
          <SelectField
            id="retention-policy"
            bind:value={retentionSettings.policy}
            label="Retention Policy"
            options={retentionPolicyOptions}
            disabled={store.isLoading || store.isSaving}
            onchange={updateRetentionPolicy}
          />

          <!-- Max Age (shown when policy is 'age') -->
          {#if retentionSettings.policy === 'age'}
            <TextInput
              id="retention-max-age"
              bind:value={retentionSettings.maxAge}
              label="Max Age"
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
              label="Max Usage (Percentage)"
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
              label="Minimum Clips"
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
                label="Keep Spectrogram Images"
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

<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import RTSPUrlInput from '$lib/desktop/components/forms/RTSPUrlInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsStore, settingsActions, audioSettings, rtspSettings } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { RTSPUrl } from '$lib/stores/settings';
  import SettingsSection from '$lib/desktop/components/ui/SettingsSection.svelte';

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
        format: 'wav' as const,
        quality: '96k',
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
      audio: { ...$audioSettings, source },
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
      audio: { ...$audioSettings, export: { ...settings.audio.export, enabled } },
    });
  }

  function updateExportFormat(format: 'wav' | 'mp3' | 'flac' | 'aac' | 'opus') {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, export: { ...settings.audio.export, format } },
    });
  }

  function updateExportQuality(quality: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, export: { ...settings.audio.export, quality } },
    });
  }

  // Update retention settings
  function updateRetentionPolicy(policy: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, retention: { ...retentionSettings, policy } },
    });
  }

  function updateRetentionMaxAge(maxAge: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, retention: { ...retentionSettings, maxAge } },
    });
  }

  function updateRetentionMaxUsage(maxUsage: string) {
    // Ensure it has % suffix
    if (!maxUsage.endsWith('%') && !isNaN(Number(maxUsage))) {
      maxUsage = maxUsage + '%';
    }
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, retention: { ...retentionSettings, maxUsage } },
    });
  }

  function updateRetentionMinClips(minClips: number) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, retention: { ...retentionSettings, minClips } },
    });
  }

  function updateRetentionKeepSpectrograms(keepSpectrograms: boolean) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings, retention: { ...retentionSettings, keepSpectrograms } },
    });
  }
</script>

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
              ...$audioSettings,
              equalizer: { ...settings.audio.equalizer, enabled: settings.audio.equalizer.enabled },
            },
          })}
      />
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
              ...$audioSettings,
              soundLevel: { ...settings.audio.soundLevel, enabled: settings.audio.soundLevel.enabled },
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
                  ...$audioSettings,
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
          <svg
            xmlns="http://www.w3.org/2000/svg"
            fill="none"
            viewBox="0 0 24 24"
            class="stroke-current shrink-0 w-6 h-6"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            ></path></svg
          >
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
                ...$audioSettings,
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
                audio: { ...$audioSettings, export: { ...settings.audio.export, path: value } },
              })}
          />

          <!-- Export Type -->
          <SelectField
            id="export-type"
            bind:value={settings.audio.export.format}
            label="Export Type"
            options={exportFormatOptions}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateExportFormat(value as 'wav' | 'mp3' | 'flac' | 'aac' | 'opus')}
          />

          <!-- Bitrate -->
          <TextInput
            id="export-bitrate"
            bind:value={settings.audio.export.quality}
            label="Bitrate"
            placeholder="96k"
            disabled={store.isLoading ||
              store.isSaving ||
              !ffmpegAvailable ||
              !['aac', 'opus', 'mp3'].includes(settings.audio.export.format)}
            onchange={updateExportQuality}
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
            bind:value={retentionSettings.minClips}
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

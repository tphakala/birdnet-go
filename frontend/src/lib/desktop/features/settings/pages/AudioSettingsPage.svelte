<!--
  Audio Settings Page Component

  Purpose: Audio configuration settings for BirdNET-Go including audio capture,
  filters, sound level monitoring, export settings, and retention policies.

  Features:
  - Tabbed interface: Sound Card, Streams, Processing, Export, Retention
  - Audio capture source selection (sound card/RTSP)
  - Audio filters and equalizer configuration
  - Sound level monitoring setup
  - Audio export format and path settings
  - Audio clip retention policies
  - Empty states for missing audio sources
  - Default tab preference persistence

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Async API loading for audio devices
  - Memoized localized options with $derived.by
  - Proper state management for API data
  - Lazy tab content rendering

  @component
-->
<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import RTSPUrlInput from '$lib/desktop/components/forms/RTSPUrlInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import InlineSlider from '$lib/desktop/components/forms/InlineSlider.svelte';
  import {
    settingsStore,
    settingsActions,
    audioSettings,
    rtspSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsTabs, {
    type TabDefinition,
  } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsCard from '$lib/desktop/features/settings/components/SettingsCard.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import { t } from '$lib/i18n';
  import { getLocale } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { getBitrateConfig, formatBitrate, parseNumericBitrate } from '$lib/utils/audioValidation';
  import { Volume2, Radio, SlidersHorizontal, FileAudio, Clock, RefreshCw } from '@lucide/svelte';

  const logger = loggers.audio;

  // Storage key for default tab preference
  const STORAGE_KEY = 'birdnet-audio-settings-default-tab';

  // Tab state management
  let activeTab = $state(localStorage.getItem(STORAGE_KEY) || 'soundcard');

  // Default tab preference (persisted to localStorage)
  let defaultTab = $state(localStorage.getItem(STORAGE_KEY) || 'soundcard');

  // Save default tab preference
  function setAsDefaultTab(tabId: string) {
    defaultTab = tabId;
    localStorage.setItem(STORAGE_KEY, tabId);
  }

  // Check if a tab is the default
  function isDefaultTab(tabId: string): boolean {
    return defaultTab === tabId;
  }

  // PERFORMANCE OPTIMIZATION: Localized option arrays - memoized to prevent unnecessary recomputations
  // These will only recompute when the locale changes, not on every reactive update
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

  // Maximum disk usage options as derived store for consistency
  let maxUsageOptions = $derived([
    { value: '70%', label: '70%' },
    { value: '75%', label: '75%' },
    { value: '80%', label: '80%' },
    { value: '85%', label: '85%' },
    { value: '90%', label: '90%' },
    { value: '95%', label: '95%' },
  ]);

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    (() => {
      const audioBase = $audioSettings || {
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
          path: 'clips/',
          type: 'wav' as const,
          bitrate: '96k',
          retention: {
            policy: 'none',
            maxAge: '7d',
            maxUsage: '80%',
            minClips: 10,
            keepSpectrograms: false,
          },
          length: 15, // Default 15 seconds capture length
          preCapture: 3, // Default 3 seconds pre-detection buffer
          gain: 0, // Default 0 dB gain (no amplification)
          normalization: {
            enabled: false, // Disabled by default
            targetLUFS: -23.0, // EBU R128 broadcast standard
            loudnessRange: 7.0, // Typical range for broadcast
            truePeak: -2.0, // Headroom to prevent clipping
          },
        },
      };

      const rtspBase = $rtspSettings || {
        transport: 'tcp',
        urls: [],
      };

      // Ensure urls is always an array even if rtspSettings exists but has undefined/null urls
      // Also ensure equalizer filters is always an array
      return {
        audio: {
          ...audioBase,
          equalizer: {
            enabled: audioBase.equalizer?.enabled ?? false,
            filters: audioBase.equalizer?.filters ?? [], // Always ensures filters is an array
          },
        },
        rtsp: {
          transport: rtspBase.transport || 'tcp',
          urls: rtspBase.urls ?? [], // Always ensures urls is an array
        },
      };
    })()
  );
  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection per tab with $derived
  // Sound Card tab changes
  let soundCardTabHasChanges = $derived(
    hasSettingsChanged(
      { source: (store.originalData as any)?.realtime?.audio?.source },
      { source: (store.formData as any)?.realtime?.audio?.source }
    )
  );

  // Streams tab changes
  let streamsTabHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.rtsp,
      (store.formData as any)?.realtime?.rtsp
    )
  );

  // Processing tab changes (equalizer + sound level)
  let processingTabHasChanges = $derived(
    hasSettingsChanged(
      {
        equalizer: (store.originalData as any)?.realtime?.audio?.equalizer,
        soundLevel: (store.originalData as any)?.realtime?.audio?.soundLevel,
      },
      {
        equalizer: (store.formData as any)?.realtime?.audio?.equalizer,
        soundLevel: (store.formData as any)?.realtime?.audio?.soundLevel,
      }
    )
  );

  // Export tab changes (excluding retention)
  let exportTabHasChanges = $derived(
    hasSettingsChanged(
      {
        enabled: (store.originalData as any)?.realtime?.audio?.export?.enabled,
        path: (store.originalData as any)?.realtime?.audio?.export?.path,
        type: (store.originalData as any)?.realtime?.audio?.export?.type,
        bitrate: (store.originalData as any)?.realtime?.audio?.export?.bitrate,
        length: (store.originalData as any)?.realtime?.audio?.export?.length,
        preCapture: (store.originalData as any)?.realtime?.audio?.export?.preCapture,
        gain: (store.originalData as any)?.realtime?.audio?.export?.gain,
        normalization: (store.originalData as any)?.realtime?.audio?.export?.normalization,
      },
      {
        enabled: (store.formData as any)?.realtime?.audio?.export?.enabled,
        path: (store.formData as any)?.realtime?.audio?.export?.path,
        type: (store.formData as any)?.realtime?.audio?.export?.type,
        bitrate: (store.formData as any)?.realtime?.audio?.export?.bitrate,
        length: (store.formData as any)?.realtime?.audio?.export?.length,
        preCapture: (store.formData as any)?.realtime?.audio?.export?.preCapture,
        gain: (store.formData as any)?.realtime?.audio?.export?.gain,
        normalization: (store.formData as any)?.realtime?.audio?.export?.normalization,
      }
    )
  );

  // Retention tab changes
  let retentionTabHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as any)?.realtime?.audio?.export?.retention,
      (store.formData as any)?.realtime?.audio?.export?.retention
    )
  );

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // Audio source options - map to actual device names
  let audioDevices = $state<ApiState<Array<{ Index: number; Name: string }>>>({
    loading: true,
    error: null,
    data: [],
  });

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // PERFORMANCE OPTIMIZATION: Load audio devices with proper state management
  $effect(() => {
    loadAudioDevices();
  });

  async function loadAudioDevices() {
    audioDevices.loading = true;
    audioDevices.error = null;

    try {
      const response = await fetch('/api/v1/settings/audio/get', {
        headers: { 'X-CSRF-Token': csrfToken },
      });
      if (!response.ok) {
        throw new Error(`Failed to load audio devices: ${response.status}`);
      }
      const data = await response.json();
      audioDevices.data = data || [];
    } catch (error) {
      logger.error('Error fetching audio devices:', error);
      audioDevices.error = t('settings.audio.errors.devicesLoadFailed');
      audioDevices.data = [];
    } finally {
      audioDevices.loading = false;
    }
  }

  // Check if ffmpeg is available
  let ffmpegAvailable = $state(true); // Assume true for now

  // Bitrate slider configuration based on format
  let bitrateConfig = $derived(getBitrateConfig(settings.audio.export.type));

  // Parse numeric bitrate from string format (e.g., "96k" -> 96)
  let numericBitrate = $derived(parseNumericBitrate(settings.audio.export.bitrate));

  // Retention settings with proper structure
  let retentionSettings = $derived({
    policy: settings.audio.export?.retention?.policy || 'none',
    maxAge: settings.audio.export?.retention?.maxAge || '7d',
    maxUsage: settings.audio.export?.retention?.maxUsage || '80%',
    minClips: settings.audio.export?.retention?.minClips || 10,
    keepSpectrograms: settings.audio.export?.retention?.keepSpectrograms || false,
  });

  // Helper function to merge RTSP settings and avoid code duplication
  function mergeRtsp(partialRtsp: Partial<{ transport: string; urls: string[] }>) {
    const storeState = $settingsStore;
    const currentRtsp = storeState.formData.realtime?.rtsp || { transport: 'tcp', urls: [] };

    settingsActions.updateSection('realtime', {
      rtsp: {
        ...currentRtsp, // Preserve all existing fields
        ...partialRtsp, // Apply partial updates
      },
    });
  }

  // Update handlers
  function updateAudioSource(source: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, source },
    });
  }

  function updateRTSPTransport(transport: string) {
    mergeRtsp({ transport });
  }

  function updateRTSPUrls(urls: string[]) {
    mergeRtsp({ urls });
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

  function updateExportBitrate(bitrate: number | string) {
    const formattedBitrate = formatBitrate(bitrate);

    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: { ...settings.audio.export, bitrate: formattedBitrate },
      },
    });
  }

  // Update retention settings
  function updateRetentionPolicy(policy: string) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: {
          ...settings.audio.export,
          retention: { ...retentionSettings, policy },
        },
      },
    });
  }

  function updateRetentionMaxAge(maxAge: string) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: {
          ...settings.audio.export,
          retention: { ...retentionSettings, maxAge },
        },
      },
    });
  }

  function updateRetentionMaxUsage(maxUsage: string) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: {
          ...settings.audio.export,
          retention: { ...retentionSettings, maxUsage },
        },
      },
    });
  }

  function updateRetentionMinClips(minClips: number) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: {
          ...settings.audio.export,
          retention: { ...retentionSettings, minClips },
        },
      },
    });
  }

  function updateRetentionKeepSpectrograms(keepSpectrograms: boolean) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        export: {
          ...settings.audio.export,
          retention: { ...retentionSettings, keepSpectrograms },
        },
      },
    });
  }

  // Handle equalizer updates from the AudioEqualizerSettings component
  function handleEqualizerUpdate(equalizerSettings: { enabled: boolean; filters: any[] }) {
    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        equalizer: equalizerSettings,
      },
    });
  }

  // Empty state helpers
  let hasAudioDevices = $derived(!audioDevices.loading && audioDevices.data.length > 0);
  let hasStreams = $derived(settings.rtsp.urls && settings.rtsp.urls.length > 0);
  let hasSelectedSoundCard = $derived(settings.audio.source && settings.audio.source.length > 0);

  // Tab definitions
  let tabs: TabDefinition[] = $derived([
    {
      id: 'soundcard',
      label: t('settings.audio.tabs.soundCard'),
      icon: Volume2,
      hasChanges: soundCardTabHasChanges,
      isDefault: isDefaultTab('soundcard'),
      showDefaultStar: true,
      content: soundCardTabContent,
    },
    {
      id: 'streams',
      label: t('settings.audio.tabs.streams'),
      icon: Radio,
      hasChanges: streamsTabHasChanges,
      isDefault: isDefaultTab('streams'),
      showDefaultStar: true,
      content: streamsTabContent,
    },
    {
      id: 'processing',
      label: t('settings.audio.tabs.processing'),
      icon: SlidersHorizontal,
      hasChanges: processingTabHasChanges,
      content: processingTabContent,
    },
    {
      id: 'export',
      label: t('settings.audio.tabs.export'),
      icon: FileAudio,
      hasChanges: exportTabHasChanges,
      content: exportTabContent,
    },
    {
      id: 'retention',
      label: t('settings.audio.tabs.retention'),
      icon: Clock,
      hasChanges: retentionTabHasChanges,
      content: retentionTabContent,
    },
  ]);
</script>

<!-- Tab Content Snippets -->
{#snippet soundCardTabContent()}
  <div class="space-y-6">
    <!-- Default Tab Toggle for Sound Card/Streams tabs -->
    {#if !isDefaultTab('soundcard')}
      <div class="flex justify-end">
        <button
          type="button"
          class="btn btn-ghost btn-xs gap-1.5 text-base-content/60 hover:text-base-content"
          onclick={() => setAsDefaultTab('soundcard')}
        >
          {t('settings.tabs.setAsDefault')}
        </button>
      </div>
    {/if}

    {#if audioDevices.loading}
      <!-- Loading State -->
      <div class="flex items-center justify-center py-12">
        <span class="loading loading-spinner loading-lg text-primary"></span>
        <span class="ml-3 text-base-content/90">{t('settings.audio.loading')}</span>
      </div>
    {:else if audioDevices.error}
      <!-- Error State -->
      <EmptyState
        icon={Volume2}
        title={t('settings.audio.emptyStates.soundCard.errorTitle')}
        description={audioDevices.error}
        primaryAction={{
          label: t('settings.audio.emptyStates.soundCard.retry'),
          icon: RefreshCw,
          onclick: loadAudioDevices,
        }}
      />
    {:else if !hasAudioDevices}
      <!-- Empty State: No Sound Cards Found -->
      <EmptyState
        icon={Volume2}
        title={t('settings.audio.emptyStates.soundCard.title')}
        description={t('settings.audio.emptyStates.soundCard.description')}
        hints={[
          t('settings.audio.emptyStates.soundCard.hints.container'),
          t('settings.audio.emptyStates.soundCard.hints.streams'),
          t('settings.audio.emptyStates.soundCard.hints.usb'),
        ]}
        hintsTitle={t('settings.audio.emptyStates.soundCard.hintsTitle')}
        primaryAction={{
          label: t('settings.audio.emptyStates.soundCard.refresh'),
          icon: RefreshCw,
          onclick: loadAudioDevices,
        }}
      />
    {:else}
      <!-- Sound Card Selection -->
      <SettingsCard>
        <SelectField
          id="audio-source"
          value={settings.audio.source}
          label={t('settings.audio.audioCapture.audioSourceLabel')}
          placeholder={t('settings.audio.audioCapture.noSoundCardCapture')}
          helpText={t('settings.audio.audioCapture.audioSourceHelp')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateAudioSource}
          options={[]}
        >
          <option value="">{t('settings.audio.audioCapture.noSoundCardCapture')}</option>
          {#each audioDevices.data as device}
            <option value={device.Name}>{device.Name}</option>
          {/each}
        </SelectField>

        {#if hasSelectedSoundCard}
          <div class="mt-4 flex items-center gap-2 text-sm text-success">
            <Volume2 class="size-4" />
            <span>{t('settings.audio.audioCapture.deviceSelected')}</span>
          </div>
        {/if}
      </SettingsCard>

      <!-- Device List -->
      <SettingsCard>
        <h4 class="text-sm font-medium text-base-content/90 mb-3">
          {t('settings.audio.audioCapture.availableDevices')}
        </h4>
        <div class="space-y-2">
          {#each audioDevices.data as device (device.Index)}
            <div
              class="flex items-center gap-3 p-3 rounded-lg bg-base-100/50 {settings.audio
                .source === device.Name
                ? 'ring-2 ring-primary/50'
                : ''}"
            >
              <div class="flex-1 min-w-0">
                <p class="text-sm font-medium truncate">{device.Name}</p>
                <p class="text-xs text-base-content/50">Index: {device.Index}</p>
              </div>
              {#if settings.audio.source === device.Name}
                <span class="badge badge-primary badge-sm"
                  >{t('settings.audio.audioCapture.active')}</span
                >
              {/if}
            </div>
          {/each}
        </div>
      </SettingsCard>
    {/if}
  </div>
{/snippet}

{#snippet streamsTabContent()}
  <div class="space-y-6">
    <!-- Default Tab Toggle for Sound Card/Streams tabs -->
    {#if !isDefaultTab('streams')}
      <div class="flex justify-end">
        <button
          type="button"
          class="btn btn-ghost btn-xs gap-1.5 text-base-content/60 hover:text-base-content"
          onclick={() => setAsDefaultTab('streams')}
        >
          {t('settings.tabs.setAsDefault')}
        </button>
      </div>
    {/if}

    {#if !hasStreams}
      <!-- Empty State: No Streams Configured -->
      <EmptyState
        icon={Radio}
        title={t('settings.audio.emptyStates.streams.title')}
        description={t('settings.audio.emptyStates.streams.description')}
        hints={[
          t('settings.audio.emptyStates.streams.hints.rtsp'),
          t('settings.audio.emptyStates.streams.hints.multiple'),
          t('settings.audio.emptyStates.streams.hints.protocol'),
        ]}
        hintsTitle={t('settings.audio.emptyStates.streams.hintsTitle')}
      />
    {/if}

    <!-- Transport Protocol -->
    <SettingsCard>
      <SelectField
        id="rtsp-transport"
        value={settings.rtsp.transport}
        label={t('settings.audio.audioCapture.rtspTransportLabel')}
        helpText={t('settings.audio.audioCapture.rtspTransportHelp')}
        options={[
          { value: 'tcp', label: t('settings.audio.transport.tcp') },
          { value: 'udp', label: t('settings.audio.transport.udp') },
        ]}
        disabled={store.isLoading || store.isSaving}
        onchange={updateRTSPTransport}
      />
    </SettingsCard>

    <!-- RTSP URLs -->
    <SettingsCard>
      <div class="form-control">
        <label class="label" for="rtsp-urls">
          <span class="label-text font-medium"
            >{t('settings.audio.audioCapture.rtspUrlsLabel')}</span
          >
        </label>
        <div id="rtsp-urls">
          <RTSPUrlInput
            urls={settings.rtsp.urls}
            onUpdate={updateRTSPUrls}
            disabled={store.isLoading || store.isSaving}
          />
        </div>
        <div class="label">
          <span class="label-text-alt text-base-content/90">
            {t('settings.audio.audioCapture.rtspUrlsHelp')}
          </span>
        </div>
      </div>
    </SettingsCard>

    <!-- Stream Status Summary -->
    {#if hasStreams}
      <SettingsCard>
        <div class="flex items-center gap-2 text-sm text-success">
          <Radio class="size-4" />
          <span>
            {t('settings.audio.audioCapture.streamsConfigured', {
              count: settings.rtsp.urls.length,
            })}
          </span>
        </div>
      </SettingsCard>
    {/if}
  </div>
{/snippet}

{#snippet processingTabContent()}
  <div class="space-y-6">
    <!-- Audio Equalizer -->
    <SettingsSection
      title={t('settings.audio.audioFilters.title')}
      description={t('settings.audio.audioFilters.description')}
      originalData={(store.originalData as any)?.realtime?.audio?.equalizer}
      currentData={(store.formData as any)?.realtime?.audio?.equalizer}
    >
      <AudioEqualizerSettings
        equalizerSettings={settings.audio.equalizer}
        disabled={store.isLoading || store.isSaving}
        onUpdate={handleEqualizerUpdate}
      />
    </SettingsSection>

    <!-- Sound Level Monitoring -->
    <SettingsSection
      title={t('settings.audio.soundLevelMonitoring.title')}
      description={t('settings.audio.soundLevelMonitoring.description')}
      originalData={(store.originalData as any)?.realtime?.audio?.soundLevel}
      currentData={(store.formData as any)?.realtime?.audio?.soundLevel}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.audio.soundLevel.enabled}
          label={t('settings.audio.soundLevelMonitoring.enable')}
          helpText={t('settings.audio.soundLevelMonitoring.enableHelp')}
          disabled={store.isLoading || store.isSaving}
          onchange={enabled =>
            settingsActions.updateSection('realtime', {
              audio: {
                ...$audioSettings!,
                soundLevel: {
                  ...settings.audio.soundLevel,
                  enabled,
                },
              },
            })}
        />

        {#if settings.audio.soundLevel.enabled}
          <div class="mt-4 pl-6">
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
            <p class="font-semibold">
              {t('settings.audio.soundLevelMonitoring.dataOutputTitle')}
            </p>
            <p class="text-base-content/90 text-sm">
              {t('settings.audio.soundLevelMonitoring.dataOutputDescription')}
            </p>
            <ul class="text-base-content/90 text-sm list-disc list-inside mt-1">
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
  </div>
{/snippet}

{#snippet exportTabContent()}
  <div class="space-y-6">
    <!-- Enable Export -->
    <SettingsCard>
      <Checkbox
        checked={settings.audio.export.enabled}
        label={t('settings.audio.audioExport.enable')}
        helpText={t('settings.audio.audioExport.enableHelp')}
        disabled={store.isLoading || store.isSaving}
        onchange={updateExportEnabled}
      />
    </SettingsCard>

    {#if settings.audio.export.enabled}
      <!-- Capture Settings -->
      <SettingsSection
        title={t('settings.audio.audioExport.captureSettings')}
        originalData={{
          length: (store.originalData as any)?.realtime?.audio?.export?.length,
          preCapture: (store.originalData as any)?.realtime?.audio?.export?.preCapture,
          gain: (store.originalData as any)?.realtime?.audio?.export?.gain,
        }}
        currentData={{
          length: settings.audio.export.length,
          preCapture: settings.audio.export.preCapture,
          gain: settings.audio.export.gain,
        }}
      >
        <div class="settings-form-grid">
          <!-- Capture Length -->
          <InlineSlider
            label={t('settings.audio.audioExport.lengthLabel')}
            value={settings.audio.export.length}
            onUpdate={value => {
              // If reducing capture length, also adjust pre-detection buffer if needed
              const maxPreCapture = Math.floor(value / 2);
              if (settings.audio.export.preCapture > maxPreCapture) {
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: {
                      ...settings.audio.export,
                      length: value,
                      preCapture: maxPreCapture,
                    },
                  },
                });
              } else {
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: { ...settings.audio.export, length: value },
                  },
                });
              }
            }}
            min={10}
            max={60}
            step={1}
            size="sm"
            unit="s"
            disabled={store.isLoading || store.isSaving}
            formatValue={(v: number) => `${v}s`}
            helpText={t('settings.audio.audioExport.lengthHelp')}
          />

          <!-- Pre-Detection Buffer -->
          <InlineSlider
            label={t('settings.audio.audioExport.preCaptureLabel')}
            value={settings.audio.export.preCapture}
            onUpdate={value =>
              settingsActions.updateSection('realtime', {
                audio: {
                  ...$audioSettings!,
                  export: { ...settings.audio.export, preCapture: value },
                },
              })}
            min={0}
            max={Math.floor(settings.audio.export.length / 2)}
            step={1}
            size="sm"
            unit="s"
            disabled={store.isLoading || store.isSaving}
            formatValue={(v: number) => `${v}s`}
            helpText={t('settings.audio.audioExport.preCaptureHelp', {
              max: Math.floor(settings.audio.export.length / 2),
            })}
          />

          <!-- Gain -->
          <InlineSlider
            label={t('settings.audio.audioExport.gainLabel')}
            value={settings.audio.export.gain}
            onUpdate={value =>
              settingsActions.updateSection('realtime', {
                audio: {
                  ...$audioSettings!,
                  export: { ...settings.audio.export, gain: value },
                },
              })}
            min={0}
            max={20}
            step={1}
            size="sm"
            unit="dB"
            disabled={store.isLoading || store.isSaving}
            formatValue={(v: number) => `${v} dB`}
            helpText={t('settings.audio.audioExport.gainHelp')}
          />
        </div>
      </SettingsSection>

      <!-- Audio Normalization -->
      <SettingsSection
        title={t('settings.audio.audioExport.normalization')}
        originalData={(store.originalData as any)?.realtime?.audio?.export?.normalization}
        currentData={settings.audio.export.normalization}
      >
        <Checkbox
          bind:checked={settings.audio.export.normalization.enabled}
          label={t('settings.audio.audioExport.normalizationEnable')}
          helpText={t('settings.audio.audioExport.normalizationHelp')}
          disabled={store.isLoading || store.isSaving}
          onchange={() =>
            settingsActions.updateSection('realtime', {
              audio: {
                ...$audioSettings!,
                export: {
                  ...settings.audio.export,
                  normalization: {
                    ...settings.audio.export.normalization,
                    enabled: settings.audio.export.normalization.enabled,
                  },
                },
              },
            })}
        />

        {#if settings.audio.export.normalization.enabled}
          <div class="settings-form-grid mt-4 pl-6">
            <!-- Target LUFS -->
            <NumberField
              label={t('settings.audio.audioExport.targetLUFSLabel')}
              value={settings.audio.export.normalization.targetLUFS}
              onUpdate={value =>
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: {
                      ...settings.audio.export,
                      normalization: {
                        ...settings.audio.export.normalization,
                        targetLUFS: value,
                      },
                    },
                  },
                })}
              min={-40}
              max={-10}
              step={0.5}
              placeholder="-23"
              helpText={t('settings.audio.audioExport.targetLUFSHelp')}
              disabled={store.isLoading || store.isSaving}
            />

            <!-- Loudness Range -->
            <NumberField
              label={t('settings.audio.audioExport.loudnessRangeLabel')}
              value={settings.audio.export.normalization.loudnessRange}
              onUpdate={value =>
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: {
                      ...settings.audio.export,
                      normalization: {
                        ...settings.audio.export.normalization,
                        loudnessRange: value,
                      },
                    },
                  },
                })}
              min={0}
              max={20}
              step={0.5}
              placeholder="7"
              helpText={t('settings.audio.audioExport.loudnessRangeHelp')}
              disabled={store.isLoading || store.isSaving}
            />

            <!-- True Peak -->
            <NumberField
              label={t('settings.audio.audioExport.truePeakLabel')}
              value={settings.audio.export.normalization.truePeak}
              onUpdate={value =>
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: {
                      ...settings.audio.export,
                      normalization: { ...settings.audio.export.normalization, truePeak: value },
                    },
                  },
                })}
              min={-10}
              max={0}
              step={0.1}
              placeholder="-2"
              helpText={t('settings.audio.audioExport.truePeakHelp')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <SettingsNote className="mt-4">
            <p class="font-semibold">{t('settings.audio.audioExport.normalizationNote')}</p>
            <p class="text-base-content/90 text-sm">
              {t('settings.audio.audioExport.normalizationNoteDescription')}
            </p>
          </SettingsNote>
        {/if}
      </SettingsSection>

      <!-- File Settings -->
      <SettingsSection
        title={t('settings.audio.audioExport.fileSettings')}
        originalData={{
          path: (store.originalData as any)?.realtime?.audio?.export?.path,
          type: (store.originalData as any)?.realtime?.audio?.export?.type,
          bitrate: (store.originalData as any)?.realtime?.audio?.export?.bitrate,
        }}
        currentData={{
          path: settings.audio.export.path,
          type: settings.audio.export.type,
          bitrate: settings.audio.export.bitrate,
        }}
      >
        <div class="settings-form-grid">
          <!-- Export Path -->
          <TextInput
            id="export-path"
            bind:value={settings.audio.export.path}
            label={t('settings.audio.audioExport.pathLabel')}
            placeholder="clips/"
            helpText={t('settings.audio.audioExport.pathHelp')}
            disabled={store.isLoading || store.isSaving}
            onchange={() =>
              settingsActions.updateSection('realtime', {
                audio: {
                  ...$audioSettings!,
                  export: { ...settings.audio.export, path: settings.audio.export.path },
                },
              })}
          />

          <!-- Export Type -->
          <SelectField
            id="export-type"
            value={settings.audio.export.type}
            label={t('settings.audio.audioExport.typeLabel')}
            helpText={t('settings.audio.audioExport.typeHelp')}
            options={exportFormatOptions}
            disabled={store.isLoading || store.isSaving}
            onchange={value => updateExportFormat(value as 'wav' | 'mp3' | 'flac' | 'aac' | 'opus')}
          />

          <!-- Bitrate -->
          {#if bitrateConfig}
            <InlineSlider
              label={t('settings.audio.audioExport.bitrateLabel')}
              value={numericBitrate}
              onUpdate={updateExportBitrate}
              min={bitrateConfig.min}
              max={bitrateConfig.max}
              step={bitrateConfig.step}
              size="sm"
              unit="k"
              disabled={store.isLoading || store.isSaving || !ffmpegAvailable}
              formatValue={(v: number) => `${v}k`}
              helpText={t('settings.audio.audioExport.bitrateHelp', {
                min: bitrateConfig.min,
                max: bitrateConfig.max,
              })}
            />
          {:else}
            <!-- Show disabled field for lossless formats -->
            <div class="form-control">
              <label class="label" for="export-bitrate-disabled">
                <span class="label-text">{t('settings.audio.audioExport.bitrateLabel')}</span>
              </label>
              <input
                id="export-bitrate-disabled"
                type="text"
                class="input input-sm input-disabled w-full"
                value="N/A - Lossless"
                disabled
                aria-describedby="lossless-note"
              />
              <div class="label" id="lossless-note">
                <span class="label-text-alt text-base-content/90">
                  {t('settings.audio.audioExport.losslessNote')}
                </span>
              </div>
            </div>
          {/if}
        </div>
      </SettingsSection>
    {:else}
      <!-- Export Disabled Info -->
      <SettingsCard>
        <p class="text-sm text-base-content/90">
          {t('settings.audio.audioExport.disabledInfo')}
        </p>
      </SettingsCard>
    {/if}
  </div>
{/snippet}

{#snippet retentionTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.audio.audioClipRetention.title')}
      description={t('settings.audio.audioClipRetention.description')}
      originalData={(store.originalData as any)?.realtime?.audio?.export?.retention}
      currentData={(store.formData as any)?.realtime?.audio?.export?.retention}
    >
      <div class="space-y-4">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <!-- Retention Policy -->
          <SelectField
            id="retention-policy"
            value={retentionSettings.policy}
            label={t('settings.audio.audioClipRetention.policyLabel')}
            helpText={t('settings.audio.audioClipRetention.policyHelp')}
            options={retentionPolicyOptions}
            disabled={store.isLoading || store.isSaving}
            onchange={updateRetentionPolicy}
          />

          <!-- Max Age (shown when policy is 'age') -->
          {#if retentionSettings.policy === 'age'}
            <TextInput
              id="retention-max-age"
              value={retentionSettings.maxAge}
              label={t('settings.audio.audioClipRetention.maxAgeLabel')}
              placeholder="7d"
              helpText={t('settings.audio.audioClipRetention.maxAgeHelp')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateRetentionMaxAge}
            />
          {/if}

          <!-- Max Usage (shown when policy is 'usage') -->
          {#if retentionSettings.policy === 'usage'}
            <SelectField
              id="retention-max-usage"
              value={retentionSettings.maxUsage}
              label={t('settings.audio.audioClipRetention.maxUsageLabel')}
              helpText={t('settings.audio.audioClipRetention.maxUsageHelp')}
              options={maxUsageOptions}
              disabled={store.isLoading || store.isSaving}
              onchange={updateRetentionMaxUsage}
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
              helpText={t('settings.audio.audioClipRetention.minClipsHelp')}
              disabled={store.isLoading || store.isSaving}
            />

            <!-- Keep Spectrograms -->
            <div class="mt-8">
              <Checkbox
                checked={retentionSettings.keepSpectrograms}
                label={t('settings.audio.audioClipRetention.keepSpectrograms')}
                helpText={t('settings.audio.audioClipRetention.keepSpectrogramsHelp')}
                disabled={store.isLoading || store.isSaving}
                onchange={updateRetentionKeepSpectrograms}
              />
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Retention Policy Info -->
    {#if retentionSettings.policy === 'none'}
      <SettingsNote>
        <p class="font-semibold">{t('settings.audio.audioClipRetention.noRetentionTitle')}</p>
        <p class="text-base-content/90 text-sm">
          {t('settings.audio.audioClipRetention.noRetentionDescription')}
        </p>
      </SettingsNote>
    {:else if retentionSettings.policy === 'age'}
      <SettingsNote>
        <p class="font-semibold">{t('settings.audio.audioClipRetention.ageRetentionTitle')}</p>
        <p class="text-base-content/90 text-sm">
          {t('settings.audio.audioClipRetention.ageRetentionDescription')}
        </p>
      </SettingsNote>
    {:else if retentionSettings.policy === 'usage'}
      <SettingsNote>
        <p class="font-semibold">{t('settings.audio.audioClipRetention.usageRetentionTitle')}</p>
        <p class="text-base-content/90 text-sm">
          {t('settings.audio.audioClipRetention.usageRetentionDescription')}
        </p>
      </SettingsNote>
    {/if}
  </div>
{/snippet}

<!-- Main Content -->
<div class="settings-page-content">
  <SettingsTabs {tabs} bind:activeTab />
</div>

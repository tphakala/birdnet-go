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
  import StreamManager from '$lib/desktop/components/forms/StreamManager.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import InlineSlider from '$lib/desktop/components/forms/InlineSlider.svelte';
  import {
    settingsStore,
    settingsActions,
    audioSettings,
    rtspSettings,
    type EqualizerFilterType,
    type StreamConfig,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import SettingsTabs, {
    type TabDefinition,
  } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import { t } from '$lib/i18n';
  import { getLocale } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { getBitrateConfig, formatBitrate, parseNumericBitrate } from '$lib/utils/audioValidation';
  import { Volume2, Radio, SlidersHorizontal, FileAudio, Clock, RefreshCw } from '@lucide/svelte';
  import { api } from '$lib/utils/api';

  const logger = loggers.audio;

  // Storage key for remembering last active tab
  const STORAGE_KEY = 'birdnet-audio-settings-active-tab';

  // Tab state management
  let activeTab = $state(localStorage.getItem(STORAGE_KEY) || 'soundcard');

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

  // Audio source options derived from audio devices
  let audioSourceOptions = $derived.by(() => {
    getLocale();
    const noCapture = { value: '', label: t('settings.audio.audioCapture.noSoundCardCapture') };
    const deviceOptions = audioDevices.data.map(device => ({
      value: device.name,
      label: device.name,
    }));
    return [noCapture, ...deviceOptions];
  });

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
        streams: [],
        health: {
          healthyDataThreshold: 60,
          monitoringInterval: 30,
        },
      };

      // Ensure streams is always an array even if rtspSettings exists but has undefined/null streams
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
          streams: rtspBase.streams ?? [], // Always ensures streams is an array
          health: rtspBase.health,
        },
      };
    })()
  );
  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection per tab with $derived
  // Sound Card tab changes
  let soundCardTabHasChanges = $derived(
    hasSettingsChanged(
      { source: store.originalData.realtime?.audio?.source },
      { source: store.formData.realtime?.audio?.source }
    )
  );

  // Streams tab changes
  let streamsTabHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.rtsp?.streams,
      store.formData.realtime?.rtsp?.streams
    )
  );

  // Audio Normalization section changes (moved here for dependency order)
  let normalizationHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.audio?.export?.normalization,
      store.formData.realtime?.audio?.export?.normalization
    )
  );

  // Processing tab changes (equalizer + sound level + normalization)
  let processingTabHasChanges = $derived(
    hasSettingsChanged(
      {
        equalizer: store.originalData.realtime?.audio?.equalizer,
        soundLevel: store.originalData.realtime?.audio?.soundLevel,
      },
      {
        equalizer: store.formData.realtime?.audio?.equalizer,
        soundLevel: store.formData.realtime?.audio?.soundLevel,
      }
    ) || normalizationHasChanges
  );

  // Clip Recording section changes (enable, capture settings)
  let clipRecordingHasChanges = $derived(
    hasSettingsChanged(
      {
        enabled: store.originalData.realtime?.audio?.export?.enabled,
        length: store.originalData.realtime?.audio?.export?.length,
        preCapture: store.originalData.realtime?.audio?.export?.preCapture,
        gain: store.originalData.realtime?.audio?.export?.gain,
      },
      {
        enabled: store.formData.realtime?.audio?.export?.enabled,
        length: store.formData.realtime?.audio?.export?.length,
        preCapture: store.formData.realtime?.audio?.export?.preCapture,
        gain: store.formData.realtime?.audio?.export?.gain,
      }
    )
  );

  // File Settings section changes
  let fileSettingsHasChanges = $derived(
    hasSettingsChanged(
      {
        path: store.originalData.realtime?.audio?.export?.path,
        type: store.originalData.realtime?.audio?.export?.type,
        bitrate: store.originalData.realtime?.audio?.export?.bitrate,
      },
      {
        path: store.formData.realtime?.audio?.export?.path,
        type: store.formData.realtime?.audio?.export?.type,
        bitrate: store.formData.realtime?.audio?.export?.bitrate,
      }
    )
  );

  // Combined recording tab changes (for tab indicator) - normalization moved to Processing
  let recordingTabHasChanges = $derived(clipRecordingHasChanges || fileSettingsHasChanges);

  // Retention tab changes
  let retentionTabHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.realtime?.audio?.export?.retention,
      store.formData.realtime?.audio?.export?.retention
    )
  );

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // Audio source options - map to actual device names
  // Note: v2 API returns lowercase field names (index, name, id)
  let audioDevices = $state<ApiState<Array<{ index: number; name: string; id: string }>>>({
    loading: true,
    error: null,
    data: [],
  });

  // PERFORMANCE OPTIMIZATION: Load audio devices with proper state management
  $effect(() => {
    loadAudioDevices();
  });

  async function loadAudioDevices() {
    audioDevices.loading = true;
    audioDevices.error = null;

    try {
      interface AudioDevice {
        index: number;
        name: string;
        id: string;
      }
      const data = await api.get<AudioDevice[]>('/api/v2/system/audio/devices');
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

  // Update handlers
  function updateAudioSource(source: string) {
    settingsActions.updateSection('realtime', {
      audio: { ...$audioSettings!, source },
    });
  }

  function updateRTSPStreams(streams: StreamConfig[]) {
    const storeState = $settingsStore;
    const currentRtsp = storeState.formData.realtime?.rtsp || { streams: [] };

    settingsActions.updateSection('realtime', {
      rtsp: {
        ...currentRtsp,
        streams,
      },
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
  // Note: Filter type matches component's local interface with optional id
  function handleEqualizerUpdate(equalizerSettings: {
    enabled: boolean;
    filters: Array<{
      id?: string;
      type: EqualizerFilterType;
      frequency: number;
      q?: number;
      width?: number;
      gain?: number;
      passes?: number;
    }>;
  }) {
    // Transform filters to ensure all have an id (required by store type)
    const transformedSettings = {
      enabled: equalizerSettings.enabled,
      filters: equalizerSettings.filters.map((filter, index) => ({
        ...filter,
        id: filter.id || `filter-${Date.now()}-${index}`,
      })),
    };

    settingsActions.updateSection('realtime', {
      audio: {
        ...$audioSettings!,
        equalizer: transformedSettings,
      },
    });
  }

  // Empty state helpers
  let hasAudioDevices = $derived(!audioDevices.loading && audioDevices.data.length > 0);
  let hasSelectedSoundCard = $derived(settings.audio.source && settings.audio.source.length > 0);

  // Tab definitions - Processing moved to last as advanced settings
  let tabs: TabDefinition[] = $derived([
    {
      id: 'soundcard',
      label: t('settings.audio.tabs.soundCard'),
      icon: Volume2,
      hasChanges: soundCardTabHasChanges,
      content: soundCardTabContent,
    },
    {
      id: 'streams',
      label: t('settings.audio.tabs.streams'),
      icon: Radio,
      hasChanges: streamsTabHasChanges,
      content: streamsTabContent,
    },
    {
      id: 'recording',
      label: t('settings.audio.tabs.recording'),
      icon: FileAudio,
      hasChanges: recordingTabHasChanges,
      content: recordingTabContent,
    },
    {
      id: 'retention',
      label: t('settings.audio.tabs.retention'),
      icon: Clock,
      hasChanges: retentionTabHasChanges,
      content: retentionTabContent,
    },
    {
      id: 'processing',
      label: t('settings.audio.tabs.processing'),
      icon: SlidersHorizontal,
      hasChanges: processingTabHasChanges,
      content: processingTabContent,
    },
  ]);
</script>

<!-- Tab Content Snippets -->
{#snippet soundCardTabContent()}
  <div class="space-y-6">
    {#if audioDevices.loading}
      <!-- Loading State -->
      <div class="flex items-center justify-center py-12">
        <span
          class="inline-block w-10 h-10 border-2 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
        ></span>
        <span class="ml-3 text-[var(--color-base-content)] opacity-90"
          >{t('settings.audio.loading')}</span
        >
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
      <SettingsSection
        title={t('settings.audio.audioCapture.title')}
        description={t('settings.audio.audioCapture.description')}
        originalData={{ source: store.originalData.realtime?.audio?.source }}
        currentData={{ source: store.formData.realtime?.audio?.source }}
      >
        <div class="space-y-4">
          <SelectDropdown
            value={settings.audio.source}
            label={t('settings.audio.audioCapture.audioSourceLabel')}
            placeholder={t('settings.audio.audioCapture.noSoundCardCapture')}
            helpText={t('settings.audio.audioCapture.audioSourceHelp')}
            disabled={store.isLoading || store.isSaving}
            onChange={value => updateAudioSource(value as string)}
            options={audioSourceOptions}
            groupBy={false}
            menuSize="sm"
          />

          <!-- Status indicator -->
          <div class="flex items-center justify-between">
            {#if hasSelectedSoundCard}
              <div class="flex items-center gap-2 text-sm text-[var(--color-success)]">
                <Volume2 class="size-4" />
                <span>{t('settings.audio.audioCapture.deviceSelected')}</span>
              </div>
            {:else}
              <div
                class="flex items-center gap-2 text-sm text-[var(--color-base-content)] opacity-60"
              >
                <Volume2 class="size-4" />
                <span>{t('settings.audio.audioCapture.noDeviceSelected')}</span>
              </div>
            {/if}
            <button
              type="button"
              class="inline-flex items-center justify-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md cursor-pointer transition-all bg-transparent hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={loadAudioDevices}
              disabled={audioDevices.loading}
            >
              <RefreshCw class={`size-3.5 ${audioDevices.loading ? 'animate-spin' : ''}`} />
              {t('settings.audio.audioCapture.refreshDevices')}
            </button>
          </div>
        </div>
      </SettingsSection>
    {/if}
  </div>
{/snippet}

{#snippet streamsTabContent()}
  <div class="space-y-6">
    <!-- RTSP Stream Configuration -->
    <SettingsSection
      title={t('settings.audio.rtspStreams.title')}
      description={t('settings.audio.rtspStreams.description')}
      originalData={store.originalData.realtime?.rtsp?.streams}
      currentData={store.formData.realtime?.rtsp?.streams}
    >
      <StreamManager
        streams={settings.rtsp.streams}
        disabled={store.isLoading || store.isSaving}
        onUpdateStreams={updateRTSPStreams}
      />
    </SettingsSection>
  </div>
{/snippet}

{#snippet processingTabContent()}
  <div class="space-y-6">
    <!-- Audio Normalization (applies to saved clips) -->
    <SettingsSection
      title={t('settings.audio.audioNormalization.title')}
      description={t('settings.audio.audioNormalization.description')}
      originalData={store.originalData.realtime?.audio?.export?.normalization}
      currentData={store.formData.realtime?.audio?.export?.normalization}
    >
      {#if !settings.audio.export.enabled}
        <!-- Dependency notice when recording is disabled -->
        <div
          class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-info)_15%,transparent)] text-[var(--color-info)] mb-4"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            fill="none"
            viewBox="0 0 24 24"
            class="stroke-current shrink-0 w-6 h-6"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            ></path>
          </svg>
          <span>{t('settings.audio.audioNormalization.requiresRecording')}</span>
        </div>
      {/if}
      <fieldset
        disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
        class="contents"
        aria-describedby="normalization-section-status"
      >
        <span id="normalization-section-status" class="sr-only">
          {settings.audio.export.enabled
            ? t('settings.audio.audioNormalization.title')
            : t('settings.audio.clipRecording.disabled')}
        </span>
        <div
          class="space-y-4 transition-opacity duration-200"
          class:opacity-50={!settings.audio.export.enabled}
        >
          <Checkbox
            bind:checked={settings.audio.export.normalization.enabled}
            label={t('settings.audio.audioNormalization.enable')}
            helpText={t('settings.audio.audioNormalization.enableHelp')}
            disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
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

          <!-- Nested fieldset for normalization parameters -->
          <fieldset
            disabled={!settings.audio.export.normalization.enabled ||
              !settings.audio.export.enabled ||
              store.isLoading ||
              store.isSaving}
            class="contents"
            aria-describedby="normalization-params-status"
          >
            <span id="normalization-params-status" class="sr-only">
              {settings.audio.export.normalization.enabled
                ? t('settings.audio.audioNormalization.enable')
                : t('settings.audio.audioNormalization.disabled')}
            </span>
            <div
              class="space-y-4 transition-opacity duration-200"
              class:opacity-50={!settings.audio.export.normalization.enabled}
            >
              <div class="settings-form-grid">
                <!-- Target LUFS -->
                <NumberField
                  label={t('settings.audio.audioNormalization.targetLUFSLabel')}
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
                  helpText={t('settings.audio.audioNormalization.targetLUFSHelp')}
                  disabled={!settings.audio.export.normalization.enabled ||
                    !settings.audio.export.enabled ||
                    store.isLoading ||
                    store.isSaving}
                />

                <!-- Loudness Range -->
                <NumberField
                  label={t('settings.audio.audioNormalization.loudnessRangeLabel')}
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
                  helpText={t('settings.audio.audioNormalization.loudnessRangeHelp')}
                  disabled={!settings.audio.export.normalization.enabled ||
                    !settings.audio.export.enabled ||
                    store.isLoading ||
                    store.isSaving}
                />

                <!-- True Peak -->
                <NumberField
                  label={t('settings.audio.audioNormalization.truePeakLabel')}
                  value={settings.audio.export.normalization.truePeak}
                  onUpdate={value =>
                    settingsActions.updateSection('realtime', {
                      audio: {
                        ...$audioSettings!,
                        export: {
                          ...settings.audio.export,
                          normalization: {
                            ...settings.audio.export.normalization,
                            truePeak: value,
                          },
                        },
                      },
                    })}
                  min={-10}
                  max={0}
                  step={0.1}
                  placeholder="-2"
                  helpText={t('settings.audio.audioNormalization.truePeakHelp')}
                  disabled={!settings.audio.export.normalization.enabled ||
                    !settings.audio.export.enabled ||
                    store.isLoading ||
                    store.isSaving}
                />
              </div>

              <SettingsNote>
                <p class="font-semibold">
                  {t('settings.audio.audioNormalization.noteTitle')}
                </p>
                <p class="text-[color:var(--color-base-content)] opacity-90 text-sm">
                  {t('settings.audio.audioNormalization.noteDescription')}
                </p>
              </SettingsNote>
            </div>
          </fieldset>
        </div>
      </fieldset>
    </SettingsSection>

    <!-- Audio Equalizer -->
    <SettingsSection
      title={t('settings.audio.audioFilters.title')}
      description={t('settings.audio.audioFilters.description')}
      originalData={store.originalData.realtime?.audio?.equalizer}
      currentData={store.formData.realtime?.audio?.equalizer}
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
      originalData={store.originalData.realtime?.audio?.soundLevel}
      currentData={store.formData.realtime?.audio?.soundLevel}
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

        <!-- Fieldset for accessible disabled state -->
        <fieldset
          disabled={!settings.audio.soundLevel.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="sound-level-status"
        >
          <span id="sound-level-status" class="sr-only">
            {settings.audio.soundLevel.enabled
              ? t('settings.audio.soundLevelMonitoring.enable')
              : t('settings.audio.soundLevelMonitoring.disabled')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.audio.soundLevel.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
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
                disabled={!settings.audio.soundLevel.enabled || store.isLoading || store.isSaving}
              />
            </div>

            <SettingsNote>
              <p class="font-semibold">
                {t('settings.audio.soundLevelMonitoring.dataOutputTitle')}
              </p>
              <p class="text-[color:var(--color-base-content)] opacity-90 text-sm">
                {t('settings.audio.soundLevelMonitoring.dataOutputDescription')}
              </p>
              <ul
                class="text-[color:var(--color-base-content)] opacity-90 text-sm list-disc list-inside mt-1"
              >
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
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet recordingTabContent()}
  <div class="space-y-6">
    <!-- Card 1: Clip Recording Settings -->
    <SettingsSection
      title={t('settings.audio.clipRecording.title')}
      description={t('settings.audio.clipRecording.description')}
      originalData={{
        enabled: store.originalData.realtime?.audio?.export?.enabled,
        length: store.originalData.realtime?.audio?.export?.length,
        preCapture: store.originalData.realtime?.audio?.export?.preCapture,
        gain: store.originalData.realtime?.audio?.export?.gain,
      }}
      currentData={{
        enabled: store.formData.realtime?.audio?.export?.enabled,
        length: store.formData.realtime?.audio?.export?.length,
        preCapture: store.formData.realtime?.audio?.export?.preCapture,
        gain: store.formData.realtime?.audio?.export?.gain,
      }}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.audio.export.enabled}
          label={t('settings.audio.clipRecording.enable')}
          helpText={t('settings.audio.clipRecording.enableHelp')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateExportEnabled}
        />

        <!-- Fieldset for accessible disabled state -->
        <fieldset
          disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="clip-recording-status"
        >
          <span id="clip-recording-status" class="sr-only">
            {settings.audio.export.enabled
              ? t('settings.audio.clipRecording.enable')
              : t('settings.audio.clipRecording.disabled')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.audio.export.enabled}
          >
            <h4 class="text-sm font-medium text-[var(--color-base-content)]">
              {t('settings.audio.clipRecording.captureSettings')}
            </h4>
            <div class="settings-form-grid">
              <!-- Capture Length -->
              <InlineSlider
                label={t('settings.audio.clipRecording.lengthLabel')}
                value={settings.audio.export.length}
                onUpdate={value => {
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
                disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
                formatValue={(v: number) => `${v}s`}
                helpText={t('settings.audio.clipRecording.lengthHelp')}
              />

              <!-- Pre-Detection Buffer -->
              <InlineSlider
                label={t('settings.audio.clipRecording.preCaptureLabel')}
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
                disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
                formatValue={(v: number) => `${v}s`}
                helpText={t('settings.audio.clipRecording.preCaptureHelp', {
                  max: Math.floor(settings.audio.export.length / 2),
                })}
              />

              <!-- Gain -->
              <InlineSlider
                label={t('settings.audio.clipRecording.gainLabel')}
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
                disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
                formatValue={(v: number) => `${v} dB`}
                helpText={t('settings.audio.clipRecording.gainHelp')}
              />
            </div>
          </div>
        </fieldset>
      </div>
    </SettingsSection>

    <!-- Card 2: File Settings -->
    <SettingsSection
      title={t('settings.audio.fileSettings.title')}
      description={t('settings.audio.fileSettings.description')}
      originalData={{
        path: store.originalData.realtime?.audio?.export?.path,
        type: store.originalData.realtime?.audio?.export?.type,
        bitrate: store.originalData.realtime?.audio?.export?.bitrate,
      }}
      currentData={{
        path: store.formData.realtime?.audio?.export?.path,
        type: store.formData.realtime?.audio?.export?.type,
        bitrate: store.formData.realtime?.audio?.export?.bitrate,
      }}
    >
      <fieldset
        disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
        class="contents"
        aria-describedby="file-settings-status"
      >
        <span id="file-settings-status" class="sr-only">
          {settings.audio.export.enabled
            ? t('settings.audio.fileSettings.title')
            : t('settings.audio.clipRecording.disabled')}
        </span>
        <div
          class="space-y-4 transition-opacity duration-200"
          class:opacity-50={!settings.audio.export.enabled}
        >
          <div class="settings-form-grid">
            <!-- Export Path -->
            <TextInput
              id="export-path"
              bind:value={settings.audio.export.path}
              label={t('settings.audio.fileSettings.pathLabel')}
              placeholder="clips/"
              helpText={t('settings.audio.fileSettings.pathHelp')}
              disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
              onchange={() =>
                settingsActions.updateSection('realtime', {
                  audio: {
                    ...$audioSettings!,
                    export: { ...settings.audio.export, path: settings.audio.export.path },
                  },
                })}
            />

            <!-- Export Type -->
            <SelectDropdown
              value={settings.audio.export.type}
              label={t('settings.audio.fileSettings.typeLabel')}
              helpText={t('settings.audio.fileSettings.typeHelp')}
              options={exportFormatOptions}
              disabled={!settings.audio.export.enabled || store.isLoading || store.isSaving}
              onChange={value =>
                updateExportFormat(value as 'wav' | 'mp3' | 'flac' | 'aac' | 'opus')}
              groupBy={false}
              menuSize="sm"
            />

            <!-- Bitrate -->
            {#if bitrateConfig}
              <InlineSlider
                label={t('settings.audio.fileSettings.bitrateLabel')}
                value={numericBitrate}
                onUpdate={updateExportBitrate}
                min={bitrateConfig.min}
                max={bitrateConfig.max}
                step={bitrateConfig.step}
                size="sm"
                unit="k"
                disabled={!settings.audio.export.enabled ||
                  store.isLoading ||
                  store.isSaving ||
                  !ffmpegAvailable}
                formatValue={(v: number) => `${v}k`}
                helpText={t('settings.audio.fileSettings.bitrateHelp', {
                  min: bitrateConfig.min,
                  max: bitrateConfig.max,
                })}
              />
            {:else}
              <div>
                <label class="block py-1" for="export-bitrate-disabled">
                  <span class="text-sm text-[var(--color-base-content)]"
                    >{t('settings.audio.fileSettings.bitrateLabel')}</span
                  >
                </label>
                <input
                  id="export-bitrate-disabled"
                  type="text"
                  class="block w-full px-3 py-1.5 text-sm bg-[var(--color-base-100)] text-[var(--color-base-content)] border border-[var(--border-100)] rounded-md opacity-50 cursor-not-allowed"
                  value="N/A - Lossless"
                  disabled
                  aria-describedby="lossless-note"
                />
                <span
                  class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block"
                  id="lossless-note"
                >
                  {t('settings.audio.fileSettings.losslessNote')}
                </span>
              </div>
            {/if}
          </div>
        </div>
      </fieldset>
    </SettingsSection>
  </div>
{/snippet}

{#snippet retentionTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.audio.audioClipRetention.title')}
      description={t('settings.audio.audioClipRetention.description')}
      originalData={store.originalData.realtime?.audio?.export?.retention}
      currentData={store.formData.realtime?.audio?.export?.retention}
    >
      <div class="space-y-4">
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <!-- Retention Policy -->
          <SelectDropdown
            value={retentionSettings.policy}
            label={t('settings.audio.audioClipRetention.policyLabel')}
            helpText={t('settings.audio.audioClipRetention.policyHelp')}
            options={retentionPolicyOptions}
            disabled={store.isLoading || store.isSaving}
            onChange={value => updateRetentionPolicy(value as string)}
            groupBy={false}
            menuSize="sm"
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
            <SelectDropdown
              value={retentionSettings.maxUsage}
              label={t('settings.audio.audioClipRetention.maxUsageLabel')}
              helpText={t('settings.audio.audioClipRetention.maxUsageHelp')}
              options={maxUsageOptions}
              disabled={store.isLoading || store.isSaving}
              onChange={value => updateRetentionMaxUsage(value as string)}
              groupBy={false}
              menuSize="sm"
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

        <!-- Retention Policy Info -->
        {#if retentionSettings.policy === 'none'}
          <SettingsNote>
            <p class="font-semibold">{t('settings.audio.audioClipRetention.noRetentionTitle')}</p>
            <p class="text-[color:var(--color-base-content)] opacity-90 text-sm">
              {t('settings.audio.audioClipRetention.noRetentionDescription')}
            </p>
          </SettingsNote>
        {:else if retentionSettings.policy === 'age'}
          <SettingsNote>
            <p class="font-semibold">{t('settings.audio.audioClipRetention.ageRetentionTitle')}</p>
            <p class="text-[color:var(--color-base-content)] opacity-90 text-sm">
              {t('settings.audio.audioClipRetention.ageRetentionDescription')}
            </p>
          </SettingsNote>
        {:else if retentionSettings.policy === 'usage'}
          <SettingsNote>
            <p class="font-semibold">
              {t('settings.audio.audioClipRetention.usageRetentionTitle')}
            </p>
            <p class="text-[color:var(--color-base-content)] opacity-90 text-sm">
              {t('settings.audio.audioClipRetention.usageRetentionDescription')}
            </p>
          </SettingsNote>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<div class="settings-page-content">
  <SettingsTabs {tabs} bind:activeTab />
</div>

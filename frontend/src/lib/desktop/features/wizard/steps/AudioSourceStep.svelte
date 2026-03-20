<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore, type RealtimeSettings } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { Mic, Video } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange }: WizardStepProps = $props();

  type SourceType = 'soundcard' | 'rtsp';

  let sourceType = $state<SourceType>('soundcard');
  let selectedDevice = $state('');
  let rtspUrl = $state('');
  let devices = $state<Array<{ value: string; label: string }>>([]);
  let devicesLoading = $state(true);
  let dirty = $state(false);

  let isValid = $derived(
    sourceType === 'soundcard' ? selectedDevice !== '' : rtspUrl.trim() !== ''
  );

  $effect(() => {
    // Read isValid (tracked), but untrack the callback to avoid re-run if parent recreates it
    const valid = isValid;
    untrack(() => onValidChange?.(valid));
  });

  onMount(() => {
    // Load current settings
    const store = get(settingsStore);
    const currentSource = store?.formData?.realtime?.audio?.source;
    if (currentSource) {
      selectedDevice = currentSource;
    }
    const currentStreams = store?.formData?.realtime?.rtsp?.streams;
    if (currentStreams && currentStreams.length > 0) {
      rtspUrl = currentStreams[0].url ?? '';
      if (!currentSource && rtspUrl) {
        sourceType = 'rtsp';
      }
    }

    // Fetch audio devices
    api
      .get<Array<{ name: string; index: number; id: string }>>('/api/v2/system/audio/devices')
      .then(data => {
        // Use index as unique key to avoid duplicate name issues with ALSA sub-devices
        devices = (data ?? []).map(d => ({
          value: d.name,
          label: d.index >= 0 ? `${d.name} (#${d.index})` : d.name,
        }));
        if (devices.length === 0 && !selectedDevice) {
          sourceType = 'rtsp';
        }
      })
      .catch(err => {
        logger.error('Failed to load audio devices', err);
        devices = [];
        if (!selectedDevice) {
          sourceType = 'rtsp';
        }
      })
      .finally(() => {
        devicesLoading = false;
      });
  });

  function setSourceType(type: SourceType) {
    sourceType = type;
    dirty = true;
  }

  function setDevice(value: string | string[]) {
    if (typeof value === 'string') {
      selectedDevice = value;
      dirty = true;
    }
  }

  // Save on unmount — only if user made changes
  $effect(() => {
    return () => {
      if (!dirty) return;
      if (sourceType === 'soundcard' && selectedDevice) {
        settingsActions.updateSection('realtime', {
          audio: { source: selectedDevice } as RealtimeSettings['audio'],
        });
      } else if (sourceType === 'rtsp' && rtspUrl.trim()) {
        settingsActions.updateSection('realtime', {
          rtsp: {
            streams: [
              {
                name: 'Stream 1',
                url: rtspUrl.trim(),
                type: 'rtsp' as const,
                transport: 'tcp' as const,
              },
            ],
          } as RealtimeSettings['rtsp'],
        });
      }
      settingsActions.saveSettings().catch(err => {
        logger.error('Failed to save audio source settings', err);
      });
    };
  });
</script>

<div class="space-y-5">
  <div>
    <span class="mb-2 block text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.audioSource.sourceTypeLabel')}
    </span>
    <div
      class="grid grid-cols-2 gap-3"
      role="radiogroup"
      aria-label={t('wizard.steps.audioSource.sourceTypeLabel')}
    >
      <button
        type="button"
        role="radio"
        aria-checked={sourceType === 'soundcard'}
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType ===
        'soundcard'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => setSourceType('soundcard')}
      >
        <Mic class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.soundcard')}
        </span>
      </button>

      <button
        type="button"
        role="radio"
        aria-checked={sourceType === 'rtsp'}
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType ===
        'rtsp'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => setSourceType('rtsp')}
      >
        <Video class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.rtspStream')}
        </span>
      </button>
    </div>
  </div>

  {#if sourceType === 'soundcard'}
    <div>
      <label
        for="wizard-audio-device"
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.audioSource.deviceLabel')}
      </label>
      {#if devicesLoading}
        <p class="text-sm text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.audioSource.deviceLoading')}
        </p>
      {:else if devices.length === 0}
        <p class="rounded-lg bg-[var(--color-info)]/10 p-3 text-sm text-[var(--color-info)]">
          {t('wizard.steps.audioSource.noDevicesFound')}
        </p>
      {:else}
        <SelectDropdown
          id="wizard-audio-device"
          options={devices}
          value={selectedDevice}
          searchable={true}
          onChange={setDevice}
        />
      {/if}
    </div>
  {/if}

  {#if sourceType === 'rtsp'}
    <div>
      <label
        for="wizard-rtsp-url"
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.audioSource.rtspUrlLabel')}
      </label>
      <p class="mb-2 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.audioSource.rtspUrlHelp')}
      </p>
      <TextInput
        id="wizard-rtsp-url"
        bind:value={rtspUrl}
        placeholder={t('wizard.steps.audioSource.rtspUrlPlaceholder')}
        oninput={() => {
          dirty = true;
        }}
      />
    </div>
  {/if}

  <p class="text-xs text-[var(--color-base-content)] opacity-50">
    {t('wizard.steps.audioSource.additionalSourcesHint')}
  </p>
</div>

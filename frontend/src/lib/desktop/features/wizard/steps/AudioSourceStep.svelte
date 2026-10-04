<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore, StreamTypes } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { Mic, Video } from '@lucide/svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  // Name of the stream the wizard creates for an RTSP source
  const WIZARD_STREAM_NAME = 'Stream 1';

  type SourceType = 'soundcard' | 'rtsp';

  let sourceType = $state<SourceType>('soundcard');
  let selectedDevice = $state('');
  let rtspUrl = $state('');
  let devices = $state<Array<{ value: string; label: string }>>([]);
  let devicesLoading = $state(true);
  let dirty = $state(false);

  let skipped = $state(false);

  let isValid = $derived(
    skipped ||
      (sourceType === 'soundcard'
        ? selectedDevice !== '' && devices.some(d => d.value === selectedDevice)
        : rtspUrl.trim() !== '')
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
          value: d.id,
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
    skipped = false;
    dirty = true;
  }

  function setDevice(value: string | string[]) {
    if (typeof value === 'string') {
      selectedDevice = value;
      skipped = false;
      dirty = true;
    }
  }

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Only runs if the user made changes and has valid data.
  async function commit(): Promise<void> {
    if (!dirty || skipped) return;
    if (sourceType === 'soundcard' && selectedDevice) {
      await settingsActions.saveSection('audio', { source: selectedDevice });
    } else if (sourceType === 'rtsp' && rtspUrl.trim()) {
      // The PATCH replaces the streams array without frontend coercion, so every
      // field the stream needs, including enabled, is sent explicitly.
      await settingsActions.saveSection('rtsp', {
        streams: [
          {
            name: WIZARD_STREAM_NAME,
            url: rtspUrl.trim(),
            enabled: true,
            type: StreamTypes.RTSP,
            transport: 'tcp',
          },
        ],
      });
    } else {
      return;
    }
    dirty = false;
  }
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
        <Video
          class="size-5 shrink-0 {sourceType === 'rtsp'
            ? 'text-[var(--color-primary)]'
            : 'text-[var(--color-base-content)] opacity-70'}"
        />
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
        <p role="status" class="text-sm text-[var(--color-base-content)] opacity-80">
          {t('wizard.steps.audioSource.deviceLoading')}
        </p>
      {:else if devices.length === 0}
        <SettingsNote className="mt-0">
          <p>{t('wizard.steps.audioSource.noDevicesFound')}</p>
        </SettingsNote>
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
      <p class="mb-2 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.audioSource.rtspUrlHelp')}
      </p>
      <TextInput
        id="wizard-rtsp-url"
        bind:value={rtspUrl}
        placeholder={t('wizard.steps.audioSource.rtspUrlPlaceholder')}
        oninput={() => {
          skipped = false;
          dirty = true;
        }}
      />
    </div>
  {/if}

  <div class="flex items-center justify-between">
    <p class="text-sm text-[var(--color-base-content)] opacity-80">
      {t('wizard.steps.audioSource.additionalSourcesHint')}
    </p>
    {#if !isValid || skipped}
      <button
        type="button"
        class="shrink-0 text-sm text-[var(--color-base-content)] opacity-60 transition-opacity hover:opacity-100"
        onclick={() => {
          skipped = true;
        }}
      >
        {t('wizard.steps.audioSource.configureLater')}
      </button>
    {/if}
  </div>
</div>

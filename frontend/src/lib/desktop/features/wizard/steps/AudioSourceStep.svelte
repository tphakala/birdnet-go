<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { Mic, Video } from '@lucide/svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';
  import type { SettingsSectionPayloads } from '$lib/utils/settingsApi';
  import { generateId } from '$lib/utils/uuid';
  import { deviceLabel, deviceValue, type AudioDevice } from '$lib/utils/audioDevices';
  import {
    findDevice,
    initialAudioChoice,
    isRtspUrl,
    soundCardPayloads,
    streamPayloads,
    type AudioSourceType,
  } from './audioSourceChoice';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  const AUDIO_DEVICES_ENDPOINT = '/api/v2/system/audio/devices';
  const URL_ERROR_ID = generateId('wizard-rtsp-url-error');

  // Same look as the dialog's Back button
  const SECONDARY_BUTTON_CLASS =
    'inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-4 py-2 text-sm font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)]';

  type DeviceState = 'loading' | 'ready' | 'failed';

  // What the server holds, not the form copy: arrays are sent back whole, so the
  // payloads must start from the stored arrays.
  function storedRealtime() {
    return get(settingsStore).originalData?.realtime;
  }

  const initial = initialAudioChoice(storedRealtime()?.audio, storedRealtime()?.rtsp);
  // True when the step shows a stream that no sound card accompanies: it opened on
  // the stream option (only when no sound card is configured), or it has saved a
  // stream itself. A sound card choice then turns that stream off.
  let openedInStreamMode = $state(initial.sourceType === 'rtsp');
  const hadSoundCards = initial.savedDevice !== '';
  const savedDevice = initial.savedDevice;
  // URL of the stream this step edits; follows every successful stream save so a
  // retry or a corrected URL edits the same entry instead of appending another
  let primaryStreamUrl: string | null = initial.primaryStreamUrl;

  let sourceType = $state<AudioSourceType>(initial.sourceType);
  // Persisted value (deviceValue) of the chosen device
  let selectedDevice = $state('');
  let rtspUrl = $state(initial.streamUrl);
  let devices = $state<AudioDevice[]>([]);
  let deviceState = $state<DeviceState>('loading');
  let deviceError = $state('');
  let dirty = $state(false);
  let skipped = $state(false);

  // Only the newest device load may set state
  let loadSequence = 0;

  let deviceOptions = $derived(
    devices.map(d => ({ value: deviceValue(d), label: deviceLabel(d, devices) }))
  );
  let urlError = $derived(rtspUrl.trim() !== '' && !isRtspUrl(rtspUrl));

  let validity = $derived.by((): { valid: boolean; reason?: TranslationKey } => {
    if (skipped) return { valid: true };
    if (sourceType === 'soundcard') {
      if (deviceState === 'loading') {
        return { valid: false, reason: 'wizard.steps.audioSource.deviceLoading' };
      }
      if (deviceState === 'failed') {
        return { valid: false, reason: 'wizard.steps.audioSource.reasons.devicesFailed' };
      }
      if (devices.length === 0) {
        return { valid: false, reason: 'wizard.steps.audioSource.reasons.noDevices' };
      }
      if (!deviceOptions.some(o => o.value === selectedDevice)) {
        return { valid: false, reason: 'wizard.steps.audioSource.reasons.chooseDevice' };
      }
      return { valid: true };
    }
    if (rtspUrl.trim() === '') {
      return { valid: false, reason: 'wizard.steps.audioSource.reasons.enterUrl' };
    }
    if (!isRtspUrl(rtspUrl)) {
      return { valid: false, reason: 'wizard.steps.audioSource.reasons.urlScheme' };
    }
    return { valid: true };
  });

  let deviceStatusText = $derived(
    deviceState === 'loading'
      ? t('wizard.steps.audioSource.deviceLoading')
      : deviceState === 'failed'
        ? t('wizard.steps.audioSource.devicesLoadFailed')
        : devices.length === 0
          ? t('wizard.steps.audioSource.noDevicesFound')
          : ''
  );

  $effect(() => {
    // Read validity (tracked), but untrack the callback to avoid re-run if parent recreates it
    const { valid, reason } = validity;
    untrack(() => onValidChange?.(valid, reason));
  });

  async function loadDevices(): Promise<void> {
    const sequence = ++loadSequence;
    deviceState = 'loading';
    deviceError = '';
    try {
      const data = await api.get<AudioDevice[]>(AUDIO_DEVICES_ENDPOINT);
      if (sequence !== loadSequence) return;
      devices = Array.isArray(data) ? data : [];
      deviceState = 'ready';
      if (!deviceOptions.some(o => o.value === selectedDevice)) {
        const saved = findDevice(devices, savedDevice);
        selectedDevice = saved ? deviceValue(saved) : '';
      }
    } catch (err) {
      if (sequence !== loadSequence) return;
      logger.error('Failed to load audio devices', err);
      devices = [];
      deviceError = err instanceof ApiError ? err.message : '';
      deviceState = 'failed';
    }
  }

  onMount(() => {
    void loadDevices();
  });

  // Last payload sent per section, so a repeated commit resends nothing. Cleared
  // by every edit, since a later choice can legitimately repeat an earlier payload.
  let lastSent: Partial<Record<'audio' | 'rtsp', string>> = {};

  function markEdited() {
    skipped = false;
    dirty = true;
    lastSent = {};
  }

  function setSourceType(type: AudioSourceType) {
    sourceType = type;
    markEdited();
  }

  function setDevice(value: string | string[]) {
    if (typeof value === 'string') {
      selectedDevice = value;
      markEdited();
    }
  }

  function chooseSetUpLater() {
    skipped = true;
  }

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Set when the step unmounts, so a commit still in flight sends no further sections.
  let left = false;
  onDestroy(() => {
    left = true;
  });

  async function send<S extends 'audio' | 'rtsp'>(
    section: S,
    body: SettingsSectionPayloads[S] | null
  ): Promise<void> {
    if (body === null || left) return;
    const json = JSON.stringify(body);
    // eslint-disable-next-line security/detect-object-injection -- section is 'audio' or 'rtsp'
    if (lastSent[section] === json) return;
    await settingsActions.saveSection(section, body);
    // eslint-disable-next-line security/detect-object-injection -- section is 'audio' or 'rtsp'
    lastSent[section] = json;
  }

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Only runs if the user made changes and has valid data. The payloads are built
  // from the stored settings at this moment, so a retry resends only what still
  // differs. Each choice writes the new source first and removes the old one
  // last, so a failure in between never leaves the station without a source.
  async function commit(): Promise<void> {
    if (!dirty || skipped) return;
    const realtime = storedRealtime();
    if (sourceType === 'soundcard') {
      const device = devices.find(d => deviceValue(d) === selectedDevice);
      if (device === undefined) return;
      const payloads = soundCardPayloads(
        realtime?.audio,
        realtime?.rtsp,
        device,
        openedInStreamMode,
        primaryStreamUrl
      );
      await send('audio', payloads.audio);
      await send('rtsp', payloads.rtsp);
    } else {
      const url = rtspUrl.trim();
      if (!isRtspUrl(url)) return;
      const payloads = streamPayloads(realtime?.audio, realtime?.rtsp, url, primaryStreamUrl);
      await send('rtsp', payloads.rtsp);
      if (payloads.rtsp !== null) openedInStreamMode = true;
      primaryStreamUrl = url;
      await send('audio', payloads.audio);
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

  <!-- Always rendered so a change of device state is announced -->
  <p role="status" class="sr-only">{sourceType === 'soundcard' ? deviceStatusText : ''}</p>

  {#if sourceType === 'soundcard'}
    <div>
      <label
        for="wizard-audio-device"
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.audioSource.deviceLabel')}
      </label>
      {#if deviceState === 'loading'}
        <p class="text-sm text-[var(--color-base-content)] opacity-80">
          {t('wizard.steps.audioSource.deviceLoading')}
        </p>
      {:else if deviceState === 'failed' || devices.length === 0}
        <SettingsNote className="mt-0">
          {#if deviceState === 'failed'}
            <p class="text-[var(--color-error)]">
              {t('wizard.steps.audioSource.devicesLoadFailed')}
            </p>
            {#if deviceError}
              <p class="mt-1 opacity-80">{deviceError}</p>
            {/if}
          {:else}
            <p>{t('wizard.steps.audioSource.noDevicesFound')}</p>
          {/if}
          <div class="mt-3 flex flex-wrap gap-2">
            <button type="button" class={SECONDARY_BUTTON_CLASS} onclick={() => void loadDevices()}>
              {t('common.retry')}
            </button>
            <button
              type="button"
              class={SECONDARY_BUTTON_CLASS}
              onclick={() => setSourceType('rtsp')}
            >
              {t('wizard.steps.audioSource.useStreamInstead')}
            </button>
          </div>
        </SettingsNote>
      {:else}
        <SelectDropdown
          id="wizard-audio-device"
          options={deviceOptions}
          value={selectedDevice}
          searchable={true}
          onChange={setDevice}
        />
        {#if openedInStreamMode}
          <SettingsNote className="mt-3">
            <p>{t('wizard.steps.audioSource.soundCardReplacesStream')}</p>
          </SettingsNote>
        {/if}
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
        aria-describedby={urlError ? URL_ERROR_ID : undefined}
        oninput={markEdited}
      />
      <!-- Always rendered so the error is announced when it appears -->
      <p
        id={URL_ERROR_ID}
        role="alert"
        class={urlError ? 'mt-1 text-sm text-[var(--color-error)]' : 'sr-only'}
      >
        {urlError ? t('wizard.steps.audioSource.reasons.urlScheme') : ''}
      </p>
      {#if hadSoundCards}
        <SettingsNote className="mt-3">
          <p>{t('wizard.steps.audioSource.streamReplacesSoundCards')}</p>
        </SettingsNote>
      {/if}
    </div>
  {/if}

  <div class="flex items-center justify-between gap-3">
    <p class="text-sm text-[var(--color-base-content)] opacity-80">
      {#if skipped}
        {t('wizard.steps.audioSource.setUpLaterChosen')}
      {:else}
        {t('wizard.steps.audioSource.additionalSourcesHint')}
      {/if}
    </p>
    <button
      type="button"
      class="{SECONDARY_BUTTON_CLASS} shrink-0 {skipped ? 'border-[var(--color-primary)]' : ''}"
      aria-pressed={skipped}
      onclick={chooseSetUpLater}
    >
      {t('wizard.steps.audioSource.setUpLater')}
    </button>
  </div>
</div>

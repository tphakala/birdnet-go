<script lang="ts">
  import { onDestroy, onMount, tick, untrack } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { Mic, Video } from '@lucide/svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import type { WizardStepProps } from '../types';
  import { SECONDARY_BUTTON_CLASS } from '../styles';
  import { getLogger } from '$lib/utils/logger';
  import type { SettingsSectionPayloads } from '$lib/utils/settingsApi';
  import { generateId } from '$lib/utils/uuid';
  import { deviceLabel, deviceValue, type AudioDevice } from '$lib/utils/audioDevices';
  import {
    findDevice,
    initialAudioChoice,
    isMalformedRtspUrl,
    isRtspUrl,
    soundCardPayloads,
    streamPayloads,
    type AudioSourceType,
  } from './audioSourceChoice';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  const AUDIO_DEVICES_ENDPOINT = '/api/v2/system/audio/devices';
  const URL_ERROR_ID = generateId('wizard-rtsp-url-error');
  const DEVICE_FIELD_ID = 'wizard-audio-device';
  const URL_FIELD_ID = 'wizard-rtsp-url';

  type DeviceState = 'loading' | 'ready' | 'failed';

  // What the server holds, not the form copy: arrays are sent back whole, so the
  // payloads must start from the stored arrays.
  function storedRealtime() {
    return get(settingsStore).originalData?.realtime;
  }

  const stored = storedRealtime();
  const initial = initialAudioChoice(stored?.audio, stored?.rtsp);
  // True when the step owns a stream that a later sound card choice must turn
  // off: it opened on the stream option (only when no sound card is configured)
  // or it has saved a stream itself.
  let streamOwnedByStep = $state(initial.sourceType === 'rtsp');
  // URL of the stream this step edits; follows every successful stream save so a
  // retry or a corrected URL edits the same entry instead of appending another
  let primaryStreamUrl: string | null = initial.primaryStreamUrl;

  let sourceType = $state<AudioSourceType>(initial.sourceType);
  // Persisted value (deviceValue) of the chosen device
  let selectedDevice = $state('');
  let rtspUrl = $state(initial.primaryStreamUrl ?? '');
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
  // The listed device the user has chosen, if any
  let selectedEntry = $derived(devices.find(d => deviceValue(d) === selectedDevice));

  // What the sound card option shows instead of the dropdown, and why Next waits
  const DEVICE_NOTICES = {
    loading: {
      text: 'wizard.steps.audioSource.deviceLoading',
      reason: 'wizard.steps.audioSource.deviceLoading',
    },
    failed: {
      text: 'wizard.steps.audioSource.devicesLoadFailed',
      reason: 'wizard.steps.audioSource.reasons.devicesFailed',
    },
    empty: {
      text: 'wizard.steps.audioSource.noDevicesFound',
      reason: 'wizard.steps.audioSource.reasons.noDevices',
    },
  } as const satisfies Record<string, { text: TranslationKey; reason: TranslationKey }>;

  let deviceNotice = $derived.by((): keyof typeof DEVICE_NOTICES | null => {
    if (deviceState === 'loading') return 'loading';
    if (deviceState === 'failed') return 'failed';
    return devices.length === 0 ? 'empty' : null;
  });

  // Why the chosen source is incomplete, ignoring Set up later
  let incompleteReason = $derived.by((): TranslationKey | undefined => {
    if (sourceType === 'soundcard') {
      // eslint-disable-next-line security/detect-object-injection -- deviceNotice is one of the DEVICE_NOTICES keys
      if (deviceNotice !== null) return DEVICE_NOTICES[deviceNotice].reason;
      return selectedEntry === undefined
        ? 'wizard.steps.audioSource.reasons.chooseDevice'
        : undefined;
    }
    if (rtspUrl.trim() === '') return 'wizard.steps.audioSource.reasons.enterUrl';
    return isRtspUrl(rtspUrl) ? undefined : 'wizard.steps.audioSource.reasons.urlScheme';
  });
  // Set when the user leaves the URL field and on open for a saved URL; cleared by
  // every edit. Display only: Next's reason does not depend on it.
  let urlLeft = $state(initial.primaryStreamUrl !== null);
  let showUrlError = $derived(urlLeft && sourceType === 'rtsp' && isMalformedRtspUrl(rtspUrl));
  // Primitives, so the effect below runs only when one of them changes
  let valid = $derived(skipped || incompleteReason === undefined);
  let reason = $derived(skipped ? undefined : incompleteReason);

  $effect(() => {
    // Read validity (tracked), but untrack the callback to avoid re-run if parent recreates it
    const isValid = valid;
    const why = reason;
    untrack(() => onValidChange?.(isValid, why));
  });

  // Resolves true when this load applied its result, false when a newer load superseded it
  async function loadDevices(): Promise<boolean> {
    const sequence = ++loadSequence;
    deviceState = 'loading';
    deviceError = '';
    try {
      const data = await api.get<AudioDevice[]>(AUDIO_DEVICES_ENDPOINT);
      if (sequence !== loadSequence) return false;
      devices = Array.isArray(data) ? data : [];
      deviceState = 'ready';
      if (selectedEntry === undefined) {
        const saved = findDevice(devices, initial.savedDevice);
        selectedDevice = saved ? deviceValue(saved) : '';
      }
      return true;
    } catch (err) {
      if (sequence !== loadSequence) return false;
      logger.error('Failed to load audio devices', err);
      devices = [];
      deviceError = err instanceof ApiError ? err.message : '';
      deviceState = 'failed';
      return true;
    }
  }

  onMount(() => {
    void loadDevices();
  });

  let deviceStatusRef = $state<HTMLDivElement>();
  let retryButtonRef = $state<HTMLButtonElement>();

  // The Retry button leaves the DOM while the list loads; park focus on the device
  // status so it stays in the dialog, then hand it to the dropdown or back to Retry.
  async function retryDevices() {
    deviceStatusRef?.focus();
    const applied = await loadDevices();
    if (!applied) return;
    await tick();
    // Only move focus the step parked itself; the user may have moved it meanwhile
    if (deviceStatusRef === undefined || document.activeElement !== deviceStatusRef) return;
    if (deviceNotice === null) document.getElementById(DEVICE_FIELD_ID)?.focus();
    else retryButtonRef?.focus();
  }

  async function switchToStream() {
    setSourceType('rtsp');
    await tick();
    document.getElementById(URL_FIELD_ID)?.focus();
  }

  // Last payload sent per section, so a repeated commit resends nothing. Cleared
  // by every edit, since a later choice can legitimately repeat an earlier payload.
  let lastSent: Partial<Record<'audio' | 'rtsp', string>> = {};

  function markEdited() {
    skipped = false;
    dirty = true;
    lastSent = {};
  }

  function onUrlInput() {
    markEdited();
    urlLeft = false;
  }

  function onUrlBlur() {
    urlLeft = true;
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
      const device = selectedEntry;
      if (device === undefined) return;
      const payloads = soundCardPayloads(
        realtime?.audio,
        realtime?.rtsp,
        device,
        streamOwnedByStep,
        primaryStreamUrl
      );
      await send('audio', payloads.audio);
      await send('rtsp', payloads.rtsp);
    } else {
      const url = rtspUrl.trim();
      if (!isRtspUrl(url)) return;
      const payloads = streamPayloads(realtime?.audio, realtime?.rtsp, url, primaryStreamUrl);
      await send('rtsp', payloads.rtsp);
      // A no-op payload means the stream was already enabled; the step still answers for it
      streamOwnedByStep = true;
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

  {#if sourceType === 'soundcard'}
    <div>
      <label
        for={DEVICE_FIELD_ID}
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.audioSource.deviceLabel')}
      </label>
      <!-- Always rendered so a change of device state is announced -->
      <div role="status" bind:this={deviceStatusRef} tabindex="-1" class="focus:outline-none">
        {#if deviceNotice === 'loading'}
          <p class="text-sm text-[var(--color-base-content)] opacity-80">
            {t(DEVICE_NOTICES.loading.text)}
          </p>
        {:else if deviceNotice !== null}
          <SettingsNote className="mt-0">
            {#if deviceNotice === 'failed'}
              <p class="text-[var(--color-error)]">{t(DEVICE_NOTICES.failed.text)}</p>
              {#if deviceError}
                <p class="mt-1 opacity-80">{deviceError}</p>
              {/if}
            {:else}
              <p>{t(DEVICE_NOTICES.empty.text)}</p>
            {/if}
            <div class="mt-3 flex flex-wrap gap-2">
              <button
                type="button"
                class={SECONDARY_BUTTON_CLASS}
                bind:this={retryButtonRef}
                onclick={() => void retryDevices()}
              >
                {t('common.retry')}
              </button>
              <button
                type="button"
                class={SECONDARY_BUTTON_CLASS}
                onclick={() => void switchToStream()}
              >
                {t('wizard.steps.audioSource.useStreamInstead')}
              </button>
            </div>
          </SettingsNote>
        {/if}
      </div>
      {#if deviceNotice === null}
        <SelectDropdown
          id={DEVICE_FIELD_ID}
          options={deviceOptions}
          value={selectedDevice}
          searchable={true}
          onChange={setDevice}
        />
        {#if streamOwnedByStep}
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
        for={URL_FIELD_ID}
        class="mb-1 block text-sm font-medium text-[var(--color-base-content)]"
      >
        {t('wizard.steps.audioSource.rtspUrlLabel')}
      </label>
      <p class="mb-2 text-sm text-[var(--color-base-content)] opacity-80">
        {t('wizard.steps.audioSource.rtspUrlHelp')}
      </p>
      <TextInput
        id={URL_FIELD_ID}
        bind:value={rtspUrl}
        placeholder={t('wizard.steps.audioSource.rtspUrlPlaceholder')}
        aria-describedby={showUrlError ? URL_ERROR_ID : undefined}
        aria-invalid={showUrlError ? 'true' : undefined}
        oninput={onUrlInput}
        onblur={onUrlBlur}
      />
      <!-- Always rendered with two lines reserved (the message wraps to two in the dialog;
           a longer one would still grow the line): the alert is announced when it fills,
           and showing it does not move the controls below -->
      <p id={URL_ERROR_ID} role="alert" class="mt-1 min-h-10 text-sm text-[var(--color-error)]">
        {showUrlError ? t('wizard.steps.audioSource.reasons.urlScheme') : ''}
      </p>
      {#if initial.savedDevice !== ''}
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
      onclick={() => (skipped = true)}
    >
      {t('wizard.steps.audioSource.setUpLater')}
    </button>
  </div>
</div>

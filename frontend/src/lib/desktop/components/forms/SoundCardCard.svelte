<!--
  Sound Card Card Component

  Purpose: One sound card audio source whose settings go into the settings
  form; the page's Save Changes bar persists them. The header is always visible
  and toggles the body, which holds the source's settings in groups.

  Editing:
  - Every control writes the complete changed source through onChange as it
    changes. A name is written as typed and trimmed when the field is left.
  - Device: the pick waits for the sample rate probe of that device and is then
    written together with a rate the device supports. Collapsing the card,
    disabling it (a save runs), a recreated card (Reset) or a change of the
    form's device meanwhile drops the pick. Opening the card probes the saved
    device to fill the rate list and writes nothing.

  Props:
  - source: The source as it is in the form
  - sources: Every source, for the free-device filter
  - problem: What the page's save rules report for this source
  - audioDevices: Devices the server reports
  - modelOptions: Labels of the listed models, for the header summary
  - availableModels: Models the server lists for the source editors
  - disabled: Blocks every change
  - disabledReason: Why it is disabled, shown beside delete (default: loading)
  - expanded: Whether the body is shown
  - onToggleExpanded: Called by the header button
  - onChange: Receives the complete changed source
  - onDelete: Called after the delete was confirmed

  @component
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import {
    deviceValue,
    deviceMatches,
    deviceLabel,
    type AudioDevice,
  } from '$lib/utils/audioDevices';
  import { AlertTriangle, Info, Mic, Moon, ChevronDown } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';
  import {
    fetchDeviceCapabilities as fetchCapabilities,
    coerceSupportedRate,
  } from '$lib/utils/audio/sampleRate';
  import {
    modelsFailed,
    modelsLoading,
    refreshModels,
    type BackendModel,
  } from '$lib/stores/models.svelte';
  import { acousticModelAvailability } from '$lib/stores/acousticModels.svelte';
  import { defaultModelSelection } from '$lib/utils/defaultModelSelection';
  import SelectDropdown from './SelectDropdown.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import ModelToggleList from './ModelToggleList.svelte';
  import SourceModelBadges from './SourceModelBadges.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import SourceCard from '$lib/desktop/features/settings/components/SourceCard.svelte';
  import SettingsGroup from '$lib/desktop/features/settings/components/SettingsGroup.svelte';
  import { SETTINGS_GROUP_DISCLOSURE_CLASSES } from '$lib/desktop/features/settings/components/SettingsGroupLabel.svelte';
  import TextInput from './TextInput.svelte';
  import { savedEqualizer } from './sourceFields';
  import type { SoundCardProblem } from './sourceRules';
  import type {
    AudioSourceConfig,
    EqualizerSettings,
    QuietHoursConfig,
  } from '$lib/stores/settings';
  import {
    defaultQuietHoursConfig,
    AUDIO_GAIN_MIN_DB,
    AUDIO_GAIN_MAX_DB,
  } from '$lib/stores/settings';

  interface Props {
    source: AudioSourceConfig;
    sources: AudioSourceConfig[];
    problem?: SoundCardProblem;
    audioDevices: AudioDevice[];
    modelOptions: Array<{ value: string; label: string }>;
    availableModels: BackendModel[];
    disabled?: boolean;
    disabledReason?: string;
    expanded: boolean;
    onToggleExpanded: () => void;
    onChange: (_source: AudioSourceConfig) => void;
    onDelete: () => void;
  }

  let {
    source,
    sources,
    problem,
    audioDevices,
    modelOptions,
    availableModels,
    disabled = false,
    disabledReason,
    expanded,
    onToggleExpanded,
    onChange,
    onDelete,
  }: Props = $props();

  /** Capture rate of a source that stores none (0 or absent). */
  const DEFAULT_SAMPLE_RATE = 48000;
  /** Highest rate that does not need exclusive device access on Linux. */
  const SHARED_ACCESS_MAX_RATE = 48000;
  /** Longest source name the field accepts. */
  const NAME_MAX_LENGTH = 100;
  const DEFAULT_RATE_OPTIONS = [{ value: '48000', label: '48 kHz' }];

  const cardId = generateId('soundcard');
  const nameInputId = `${cardId}-name`;
  const deviceErrorId = `${cardId}-device-error`;
  const captureHeadingId = `${cardId}-capture`;
  const modelsHeadingId = `${cardId}-models`;
  const equalizerHeadingId = `${cardId}-equalizer`;

  function change(patch: Partial<AudioSourceConfig>) {
    onChange({ ...source, ...patch });
  }

  const acousticAvailability = $derived(acousticModelAvailability());
  let defaultModels = $derived(defaultModelSelection(acousticAvailability, availableModels));
  let selectedModels = $derived(source.models ?? []);
  let listFailed = $derived(modelsFailed());
  let listLoading = $derived(modelsLoading());

  // ----- Header -----

  let deviceDisplayName = $derived(
    audioDevices.find(d => deviceMatches(d, source.device))?.name ?? source.device
  );

  const BADGE_CLASS = 'px-2 py-0.5 rounded text-xs font-semibold';

  // ----- Name -----

  // The field shows its problem once it was left, and hides it again while the
  // user edits it. A problem this field did not cause (a duplicate typed on
  // another card) shows at once, also on a card that opens with one.
  let nameTouched = $state(untrack(() => Boolean(problem?.name)));
  let nameEditing = false;
  $effect(() => {
    if (problem?.name && !nameEditing) nameTouched = true;
  });
  let nameError = $derived.by(() => {
    if (!problem?.name || !nameTouched) return undefined;
    return problem.name === 'required'
      ? t('settings.audio.soundCards.errors.nameRequired')
      : t('settings.audio.soundCards.errors.duplicateName');
  });
  let hasProblem = $derived(Boolean(problem?.name || problem?.device));
  let deviceError = $derived.by(() => {
    if (!problem?.device) return undefined;
    return problem.device === 'required'
      ? t('settings.audio.soundCards.errors.deviceRequired')
      : t('settings.audio.soundCards.errors.duplicateDevice');
  });

  function handleNameInput(name: string) {
    nameEditing = true;
    nameTouched = false;
    if (name !== source.name) change({ name });
  }

  function handleNameBlur(name: string) {
    nameEditing = false;
    nameTouched = true;
    const trimmed = name.trim();
    if (trimmed !== source.name) change({ name: trimmed });
  }

  // ----- Capture (device and sample rate) -----

  let sampleRateOptions = $state<Array<{ value: string; label: string }>>(DEFAULT_RATE_OPTIONS);
  let sampleRateVerified = $state(true);
  let sampleRateLoading = $state(false);
  // The device the user picked while its probe runs; null when none is pending.
  // The form keeps its device until the probe has returned.
  let pendingDevice = $state<string | null>(null);
  let shownDevice = $derived(pendingDevice ?? source.device);
  let currentRate = $derived(source.sampleRate || DEFAULT_SAMPLE_RATE);

  // The current source's device keeps its saved value (which may be a legacy ALSA
  // id) so the dropdown preselects it; other devices use the reboot-stable token
  // so a new pick persists the stable form (GH #3651).
  let deviceOptions = $derived(
    audioDevices
      .filter(
        d => deviceMatches(d, source.device) || !sources.some(s => deviceMatches(d, s.device))
      )
      .map(d => ({
        value: deviceMatches(d, source.device) ? source.device : deviceValue(d),
        label: deviceLabel(d, audioDevices),
      }))
  );

  function pickDevice(value: string | string[]) {
    if (typeof value !== 'string') return;
    // Picking the form's device again cancels a pending pick.
    pendingDevice = value === source.device ? null : value;
  }

  function pickRate(value: string | string[]) {
    if (typeof value === 'string') change({ sampleRate: Number(value) });
  }

  // Any move of the form's device ends a pending pick: the card's own write (which
  // the pick is then shown through) and an outside one (Reset, a reload), which
  // the pick must not write over.
  let formDevice = $derived(source.device);
  $effect(() => {
    void formDevice;
    untrack(() => {
      pendingDevice = null;
    });
  });

  // A collapsed or disabled card (a save is running) holds no pending pick.
  $effect(() => {
    if (!expanded || disabled) untrack(() => (pendingDevice = null));
  });

  // Probes the shown device while the body is open to fill the rate list. Only
  // a pick by the user writes: the device and a rate it supports, in one change.
  // Cleanup aborts a probe that the collapse, a new pick or unmount supersedes.
  $effect(() => {
    const device = shownDevice;
    const pick = untrack(() => pendingDevice);
    if (!expanded || !device) {
      // An aborted probe leaves the flag set: its finally block skips aborted runs.
      untrack(() => (sampleRateLoading = false));
      return;
    }
    const controller = new AbortController();
    untrack(() => {
      sampleRateLoading = true;
      // Drop the previous device's probed rates so a slower probe cannot leave
      // stale, unverified options on screen while the new one is in flight.
      sampleRateOptions = DEFAULT_RATE_OPTIONS;
      sampleRateVerified = true;
    });
    void (async () => {
      try {
        const result = await fetchCapabilities(device, controller.signal);
        if (controller.signal.aborted) return;
        sampleRateOptions = result.options;
        sampleRateVerified = result.verified;
        if (pick !== null) {
          change({
            device: pick,
            sampleRate: coerceSupportedRate(result.options, currentRate),
          });
        }
      } catch {
        // Only AbortError reaches here (the utility handles all other failures)
      } finally {
        if (!controller.signal.aborted) sampleRateLoading = false;
      }
    })();
    return () => controller.abort();
  });

  // ----- Equalizer -----

  let showEqualizer = $state(false);
  let equalizer = $derived<EqualizerSettings>(source.equalizer ?? { enabled: false, filters: [] });

  function changeEqualizer(updated: EqualizerSettings) {
    change({ equalizer: savedEqualizer(updated) });
  }

  // ----- Quiet hours -----

  let quietHours = $derived<QuietHoursConfig>(source.quietHours ?? { ...defaultQuietHoursConfig });

  function changeQuietHours(updated: QuietHoursConfig) {
    change({ quietHours: updated });
  }
</script>

<SourceCard
  name={source.name}
  subtitle={deviceDisplayName}
  icon={Mic}
  {expanded}
  {onToggleExpanded}
  deleteConfirm={t('settings.audio.soundCards.deleteConfirm', { name: source.name })}
  {onDelete}
  {disabled}
  disabledReason={disabledReason ?? t('common.loading')}
>
  {#snippet badges()}
    {#if hasProblem}
      <span class={cn(BADGE_CLASS, 'bg-[var(--color-error)]/15 text-[var(--text-error)]')}>
        {t('settings.audio.validation.needsAttention')}
      </span>
    {/if}
    {#if source.quietHours?.enabled}
      <span
        class={cn(
          BADGE_CLASS,
          'inline-flex items-center gap-1 bg-[var(--color-base-300)] text-[var(--color-base-content)]'
        )}
        title={t('settings.audio.quietHours.enabledBadge')}
      >
        <Moon class="size-3" aria-hidden="true" />
        {t('settings.audio.quietHours.badge')}
      </span>
    {/if}
    {#if source.gain !== 0}
      <span class={cn(BADGE_CLASS, 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]')}>
        {source.gain > 0 ? '+' : ''}{source.gain} dB
      </span>
    {/if}
    <SourceModelBadges
      {selectedModels}
      {defaultModels}
      {availableModels}
      {modelOptions}
      availability={acousticAvailability}
      {listLoading}
      {listFailed}
    />
  {/snippet}

  <!-- Name and level -->
  <div class="grid grid-cols-1 gap-x-8 gap-y-4 py-4 md:grid-cols-2">
    <SettingsGroup
      title={t('settings.audio.soundCards.nameLabel')}
      labelFor={nameInputId}
      required
      class="space-y-1.5"
    >
      <TextInput
        id={nameInputId}
        value={source.name}
        maxlength={NAME_MAX_LENGTH}
        aria-required="true"
        {disabled}
        error={nameError}
        oninput={handleNameInput}
        onblur={handleNameBlur}
      />
    </SettingsGroup>

    <SettingsGroup
      title={t('settings.audio.soundCards.levelTitle')}
      headingId="{cardId}-level"
      class="space-y-1.5"
    >
      <InlineSlider
        label={t('settings.audio.soundCards.gainLabel')}
        value={source.gain}
        updateOn="change"
        onUpdate={value => change({ gain: value })}
        min={AUDIO_GAIN_MIN_DB}
        max={AUDIO_GAIN_MAX_DB}
        step={1}
        unit=" dB"
        {disabled}
      />
    </SettingsGroup>
  </div>

  <!-- Capture -->
  <SettingsGroup
    title={t('settings.audio.soundCards.captureTitle')}
    headingId={captureHeadingId}
    class="py-4"
    titleVariant="group"
  >
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
      <div>
        <SelectDropdown
          value={shownDevice}
          label={t('settings.audio.soundCards.deviceLabel')}
          options={deviceOptions}
          required
          {disabled}
          aria-describedby={deviceError ? deviceErrorId : undefined}
          aria-invalid={Boolean(deviceError)}
          onChange={pickDevice}
          groupBy={false}
          menuSize="sm"
        />
        {#if deviceError}
          <p id={deviceErrorId} role="alert" class="mt-1 text-xs text-[var(--text-error)]">
            {deviceError}
          </p>
        {/if}
      </div>
      <div>
        <SelectDropdown
          value={String(currentRate)}
          label={t('settings.audio.soundCards.sampleRateLabel')}
          options={sampleRateOptions}
          disabled={disabled || sampleRateLoading}
          onChange={pickRate}
          groupBy={false}
          menuSize="sm"
        />
        {#if sampleRateLoading}
          <p class="mt-1 text-xs text-muted">
            {t('settings.audio.soundCards.sampleRateProbing')}
          </p>
        {:else if !sampleRateVerified}
          <p class="mt-1 flex items-center gap-1 text-xs text-[var(--text-warning)]">
            <AlertTriangle class="size-3 shrink-0" aria-hidden="true" />
            {t('settings.audio.soundCards.sampleRateUnverified')}
          </p>
        {/if}
        {#if currentRate > SHARED_ACCESS_MAX_RATE}
          <p class="mt-1 flex items-center gap-1 text-xs text-[var(--text-info)]">
            <Info class="size-3 shrink-0" aria-hidden="true" />
            {t('settings.audio.soundCards.sampleRateExclusive')}
          </p>
        {/if}
      </div>
    </div>
  </SettingsGroup>

  <!-- Classifiers -->
  <SettingsGroup
    title={t('settings.audio.soundCards.modelLabel')}
    headingId={modelsHeadingId}
    class="py-4"
    titleVariant="group"
  >
    <ModelToggleList
      models={availableModels}
      {selectedModels}
      {defaultModels}
      sourceName={source.name}
      sourceSampleRate={currentRate}
      loading={listLoading}
      failed={listFailed}
      availability={acousticAvailability}
      labelledBy={modelsHeadingId}
      {disabled}
      onChange={models => change({ models })}
      onRetry={() => void refreshModels()}
    />
  </SettingsGroup>

  <!-- Audio filters (collapsed by default) -->
  <SettingsGroup headingId={equalizerHeadingId} class="space-y-0 py-4">
    {#snippet heading()}
      <button
        id={equalizerHeadingId}
        type="button"
        class={SETTINGS_GROUP_DISCLOSURE_CLASSES}
        aria-expanded={showEqualizer}
        onclick={() => (showEqualizer = !showEqualizer)}
      >
        <ChevronDown
          class={cn(
            'size-4 transition-transform duration-200 motion-reduce:transition-none',
            showEqualizer && 'rotate-180'
          )}
          aria-hidden="true"
        />
        {t('settings.audio.audioFilters.title')}
      </button>
      <div class="flex items-center gap-3">
        {#if equalizer.enabled}
          <span class={cn(BADGE_CLASS, 'bg-[var(--color-primary)]/15 text-[var(--text-primary)]')}>
            {t('common.enabled')}
          </span>
        {/if}
      </div>
    {/snippet}
    {#if showEqualizer}
      <div class="mt-3" transition:slide={{ duration: 150 }}>
        <AudioEqualizerSettings
          equalizerSettings={equalizer}
          {disabled}
          onUpdate={changeEqualizer}
          sampleRate={currentRate}
        />
      </div>
    {/if}
  </SettingsGroup>

  <!-- Quiet hours -->
  <SettingsGroup
    title={t('settings.audio.quietHours.title')}
    headingId="{cardId}-quiet"
    class="py-4"
    titleVariant="group"
  >
    <QuietHoursEditor
      config={quietHours}
      onChange={changeQuietHours}
      {disabled}
      idPrefix="{cardId}-qh"
      enableLabel={t('settings.audio.quietHours.enableLabel')}
    />
  </SettingsGroup>
</SourceCard>

<!--
  Sound Card Card Component

  Purpose: One sound card audio source whose settings save as they change. The
  header is always visible and toggles the body; the body holds the source's
  settings in groups, each with its own save status.

  Saving:
  - Name: a local draft, committed on blur or Enter; Escape restores it. An
    empty or duplicate name is not committed and the field says why.
  - Capture (device and sample rate): a local draft with a scoped "Apply and
    restart capture", since a change restarts capture. A failed apply keeps the
    draft.
  - Gain, equalizer numbers, quiet hours times and offsets: committed after the
    auto-save debounce. Toggles, selects and model switches: committed at once.

  Props:
  - source: The saved source
  - index: Position of the source in sources
  - sources: Every source, for the duplicate checks and the free devices
  - rowId: UI-only id of the row, part of every field key
  - audioDevices: Devices the server reports
  - modelOptions: Labels of the listed models, for the header summary
  - availableModels: Models the server lists for the source editors
  - disabled: Blocks every change
  - expanded: Whether the body is shown
  - onToggleExpanded: Called by the header button
  - onCommit: Receives the complete changed source and the auto-save options
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
  import {
    AUTOSAVE_DEBOUNCE_MS,
    autoSaveStatus,
    type CommitOptions,
  } from '$lib/stores/settingsAutoSave.svelte';
  import { defaultModelSelection } from '$lib/utils/defaultModelSelection';
  import SelectDropdown from './SelectDropdown.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import ModelToggleList from './ModelToggleList.svelte';
  import SourceModelBadges from './SourceModelBadges.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import AutoSaveStatus from '$lib/desktop/features/settings/components/AutoSaveStatus.svelte';
  import SourceCard from '$lib/desktop/features/settings/components/SourceCard.svelte';
  import SettingsGroup from '$lib/desktop/features/settings/components/SettingsGroup.svelte';
  import { SETTINGS_GROUP_LABEL_CLASSES } from '$lib/desktop/features/settings/components/SettingsGroupLabel.svelte';
  import CommitTextField from '$lib/desktop/features/settings/components/CommitTextField.svelte';
  import ScopedApplyGroup from '$lib/desktop/features/settings/components/ScopedApplyGroup.svelte';
  import { ApplyDraft } from '$lib/desktop/features/settings/components/applyDraft.svelte';
  import {
    AUDIO_SOURCES_FIELD_PREFIX,
    isContinuousEqualizerChange,
    isContinuousQuietHoursChange,
    savedEqualizer,
    sourceField,
    type SourceFieldPart,
  } from './sourceFields';
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
    index: number;
    sources: AudioSourceConfig[];
    rowId: string;
    audioDevices: AudioDevice[];
    modelOptions: Array<{ value: string; label: string }>;
    availableModels: BackendModel[];
    disabled?: boolean;
    expanded: boolean;
    onToggleExpanded: () => void;
    onCommit: (_source: AudioSourceConfig, _options: CommitOptions) => void;
    onDelete: () => void;
  }

  let {
    source,
    index,
    sources,
    rowId,
    audioDevices,
    modelOptions,
    availableModels,
    disabled = false,
    expanded,
    onToggleExpanded,
    onCommit,
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
  const captureHeadingId = `${cardId}-capture`;
  const modelsHeadingId = `${cardId}-models`;
  const equalizerHeadingId = `${cardId}-equalizer`;

  function field(part: SourceFieldPart): string {
    return sourceField(AUDIO_SOURCES_FIELD_PREFIX, rowId, part);
  }

  function statusId(part: SourceFieldPart): string {
    return `${cardId}-${part}-status`;
  }

  function commit(change: Partial<AudioSourceConfig>, part: SourceFieldPart, debounce = false) {
    onCommit(
      { ...source, ...change },
      debounce ? { field: field(part), debounceMs: AUTOSAVE_DEBOUNCE_MS } : { field: field(part) }
    );
  }

  /** aria-describedby for a control: its status message while that shows an error. */
  function errorDescription(part: SourceFieldPart): string | undefined {
    return autoSaveStatus(field(part)).state === 'error' ? statusId(part) : undefined;
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
  const DISCLOSURE_LABEL_CLASS = cn(
    SETTINGS_GROUP_LABEL_CLASSES,
    'flex items-center gap-2 rounded transition-colors hover:text-[var(--text-primary)]'
  );

  // ----- Name -----

  function validateName(name: string): string | null {
    if (!name) return t('settings.audio.soundCards.errors.nameRequired');
    const lower = name.toLowerCase();
    if (sources.some((s, i) => i !== index && s.name.toLowerCase() === lower)) {
      return t('settings.audio.soundCards.errors.duplicateName');
    }
    return null;
  }

  // ----- Capture (device and sample rate, applied together) -----

  // Device and sample rate restart capture, so they are a draft with a scoped Apply.
  const capture = new ApplyDraft(
    () => ({ device: source.device, sampleRate: source.sampleRate || DEFAULT_SAMPLE_RATE }),
    untrack(() => field('capture'))
  );
  // The device alone, so the probe below reruns only when it changes, not on every draft edit.
  let draftDevice = $derived(capture.draft.device);
  let sampleRateOptions = $state<Array<{ value: string; label: string }>>(DEFAULT_RATE_OPTIONS);
  let sampleRateVerified = $state(true);
  let sampleRateLoading = $state(false);
  // Plain (non-reactive) ref: the probe effect both reads (abort) and writes this
  // controller in its synchronous prefix. As $state that would make the effect
  // depend on it and rerun at once, aborting the new probe (issue #3593).
  let fetchController: AbortController | null = null;

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

  function setDraftDevice(value: string | string[]) {
    if (typeof value !== 'string') return;
    // Back on the saved device the saved rate returns with it, so a rate the probe
    // coerced for another device does not leave a change to apply.
    capture.set(
      value === source.device
        ? { device: value, sampleRate: source.sampleRate || DEFAULT_SAMPLE_RATE }
        : { device: value }
    );
  }

  function setDraftRate(value: string | string[]) {
    if (typeof value === 'string') capture.set({ sampleRate: Number(value) });
  }

  async function fetchDeviceCapabilities(deviceId: string) {
    fetchController?.abort();
    const controller = new AbortController();
    fetchController = controller;
    sampleRateLoading = true;
    // Drop the previous device's probed rates so a slower probe cannot leave
    // stale, unverified options on screen while the new one is in flight.
    sampleRateOptions = DEFAULT_RATE_OPTIONS;
    sampleRateVerified = true;
    try {
      const result = await fetchCapabilities(deviceId, controller.signal);
      // Ignore a superseded probe: a newer device selection replaced this controller.
      if (fetchController !== controller) return;
      sampleRateOptions = result.options;
      sampleRateVerified = result.verified;
      // A changed draft gets a rate the device supports. Only a draft that still
      // equals the saved values is left alone, so opening a card never creates a
      // change to apply.
      const savedRate = source.sampleRate || DEFAULT_SAMPLE_RATE;
      if (capture.draft.device !== source.device || capture.draft.sampleRate !== savedRate) {
        const sampleRate = coerceSupportedRate(result.options, capture.draft.sampleRate);
        if (sampleRate !== capture.draft.sampleRate) capture.set({ sampleRate });
      }
    } catch {
      // Only AbortError reaches here (the utility handles all other failures)
    } finally {
      if (fetchController === controller) {
        sampleRateLoading = false;
      }
    }
  }

  // Probes the draft device while the body is open.
  $effect(() => {
    const device = draftDevice;
    if (!expanded || !device) return;
    untrack(() => void fetchDeviceCapabilities(device));
    // Capture the controller this run started so the cleanup only aborts that probe.
    const controllerToAbort = fetchController;
    return () => {
      controllerToAbort?.abort();
    };
  });

  // ----- Equalizer -----

  let showEqualizer = $state(false);
  let equalizer = $derived<EqualizerSettings>(source.equalizer ?? { enabled: false, filters: [] });

  function commitEqualizer(updated: EqualizerSettings) {
    commit(
      { equalizer: savedEqualizer(updated) },
      'equalizer',
      isContinuousEqualizerChange(updated, equalizer)
    );
  }

  // ----- Quiet hours -----

  let quietHours = $derived<QuietHoursConfig>(source.quietHours ?? { ...defaultQuietHoursConfig });

  function commitQuietHours(updated: QuietHoursConfig) {
    commit(
      { quietHours: updated },
      'quietHours',
      isContinuousQuietHoursChange(updated, quietHours)
    );
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
  disabledReason={t('common.loading')}
>
  {#snippet badges()}
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
      field={field('name')}
      statusId={statusId('name')}
      class="space-y-1.5"
    >
      <CommitTextField
        id={nameInputId}
        value={source.name}
        validate={validateName}
        onCommit={name => commit({ name }, 'name')}
        maxlength={NAME_MAX_LENGTH}
        required
        {disabled}
        describedBy={errorDescription('name')}
      />
    </SettingsGroup>

    <SettingsGroup
      title={t('settings.audio.soundCards.levelTitle')}
      headingId="{cardId}-level"
      field={field('gain')}
      statusId={statusId('gain')}
      class="space-y-1.5"
    >
      <InlineSlider
        label={t('settings.audio.soundCards.gainLabel')}
        value={source.gain}
        updateOn="change"
        onUpdate={value => commit({ gain: value }, 'gain', true)}
        min={AUDIO_GAIN_MIN_DB}
        max={AUDIO_GAIN_MAX_DB}
        step={1}
        unit=" dB"
        {disabled}
      />
    </SettingsGroup>
  </div>

  <!-- Capture: drafts with a scoped apply -->
  <SettingsGroup
    title={t('settings.audio.soundCards.captureTitle')}
    headingId={captureHeadingId}
    field={field('capture')}
    statusId={statusId('capture')}
    class="py-4"
    titleVariant="group"
  >
    <ScopedApplyGroup
      draft={capture}
      onApply={value => commit(value, 'capture')}
      hint={t('settings.audio.soundCards.captureApplyHint')}
      failedHint={t('settings.audio.soundCards.captureApplyFailed')}
      applyLabel={t('settings.audio.soundCards.applyCapture')}
      applyingLabel={t('settings.audio.soundCards.applying')}
      statusId={statusId('capture')}
      {disabled}
    >
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <SelectDropdown
          value={capture.draft.device}
          label={t('settings.audio.soundCards.deviceLabel')}
          options={deviceOptions}
          required
          {disabled}
          onChange={setDraftDevice}
          groupBy={false}
          menuSize="sm"
        />
        <div>
          <SelectDropdown
            value={String(capture.draft.sampleRate)}
            label={t('settings.audio.soundCards.sampleRateLabel')}
            options={sampleRateOptions}
            {disabled}
            onChange={setDraftRate}
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
          {#if capture.draft.sampleRate > SHARED_ACCESS_MAX_RATE}
            <p class="mt-1 flex items-center gap-1 text-xs text-[var(--text-info)]">
              <Info class="size-3 shrink-0" aria-hidden="true" />
              {t('settings.audio.soundCards.sampleRateExclusive')}
            </p>
          {/if}
        </div>
      </div>
    </ScopedApplyGroup>
  </SettingsGroup>

  <!-- Classifiers -->
  <SettingsGroup
    title={t('settings.audio.soundCards.modelLabel')}
    headingId={modelsHeadingId}
    field={field('models')}
    statusId={statusId('models')}
    class="py-4"
    titleVariant="group"
  >
    <ModelToggleList
      models={availableModels}
      {selectedModels}
      {defaultModels}
      sourceName={source.name}
      sourceSampleRate={capture.draft.sampleRate}
      loading={listLoading}
      failed={listFailed}
      availability={acousticAvailability}
      labelledBy={modelsHeadingId}
      {disabled}
      onChange={models => commit({ models }, 'models')}
      onRetry={() => void refreshModels()}
    />
  </SettingsGroup>

  <!-- Audio filters (collapsed by default) -->
  <SettingsGroup headingId={equalizerHeadingId} class="space-y-0 py-4">
    {#snippet heading()}
      <button
        id={equalizerHeadingId}
        type="button"
        class={DISCLOSURE_LABEL_CLASS}
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
        <AutoSaveStatus field={field('equalizer')} id={statusId('equalizer')} />
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
          onUpdate={commitEqualizer}
          sampleRate={capture.draft.sampleRate}
        />
      </div>
    {/if}
  </SettingsGroup>

  <!-- Quiet hours -->
  <SettingsGroup
    title={t('settings.audio.quietHours.title')}
    headingId="{cardId}-quiet"
    field={field('quietHours')}
    statusId={statusId('quietHours')}
    class="py-4"
    titleVariant="group"
  >
    <QuietHoursEditor
      config={quietHours}
      onChange={commitQuietHours}
      {disabled}
      idPrefix="{cardId}-qh"
      enableLabel={t('settings.audio.quietHours.enableLabel')}
    />
  </SettingsGroup>
</SourceCard>

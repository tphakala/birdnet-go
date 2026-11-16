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
  import { tick, untrack } from 'svelte';
  import {
    deviceValue,
    deviceMatches,
    deviceLabel,
    type AudioDevice,
  } from '$lib/utils/audioDevices';
  import {
    Trash2,
    AlertCircle,
    AlertTriangle,
    Info,
    Mic,
    Moon,
    ChevronDown,
    ChevronRight,
    RotateCcw,
  } from '@lucide/svelte';
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
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import AutoSaveStatus from '$lib/desktop/features/settings/components/AutoSaveStatus.svelte';
  import { audioSourceField, type AudioSourceFieldPart } from './audioSourceFields';
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
  const bodyId = `${cardId}-body`;
  const nameInputId = `${cardId}-name`;
  const nameErrorId = `${cardId}-name-error`;
  const captureHeadingId = `${cardId}-capture`;
  const modelsHeadingId = `${cardId}-models`;
  const deleteTitleId = `${cardId}-delete-title`;
  const deleteDescId = `${cardId}-delete-desc`;

  function field(part: AudioSourceFieldPart): string {
    return audioSourceField(rowId, part);
  }

  function statusId(part: AudioSourceFieldPart): string {
    return `${cardId}-${part}-status`;
  }

  function commit(
    change: Partial<AudioSourceConfig>,
    part: AudioSourceFieldPart,
    debounce = false
  ) {
    onCommit(
      { ...source, ...change },
      debounce ? { field: field(part), debounceMs: AUTOSAVE_DEBOUNCE_MS } : { field: field(part) }
    );
  }

  /** aria-describedby for a control: its status message while that shows an error. */
  function errorDescription(part: AudioSourceFieldPart): string | undefined {
    return autoSaveStatus(field(part)).state === 'error' ? statusId(part) : undefined;
  }

  const acousticAvailability = $derived(acousticModelAvailability());
  let defaultModels = $derived(defaultModelSelection(acousticAvailability, availableModels));
  let selectedModels = $derived(source.models ?? []);
  let hasExplicitModels = $derived(selectedModels.length > 0);
  let listFailed = $derived(modelsFailed());
  let listLoading = $derived(modelsLoading());

  // ----- Header -----

  let deviceDisplayName = $derived(
    audioDevices.find(d => deviceMatches(d, source.device))?.name ?? source.device
  );

  function modelLabels(ids: readonly string[]): string {
    return ids.map(id => modelOptions.find(m => m.value === id)?.label ?? id).join(', ');
  }

  // An empty list means "analyze with the server defaults": name them when the
  // classifier reports them, say that no model is loaded when that is the
  // verdict, hedge otherwise.
  let modelDisplayName = $derived.by(() => {
    if (hasExplicitModels) return modelLabels(selectedModels);
    if (acousticAvailability.kind === 'none') return t('settings.audio.models.noneBadge');
    if (acousticAvailability.kind === 'ready' && defaultModels.length > 0) {
      return t('settings.audio.models.defaultBadge', { models: modelLabels(defaultModels) });
    }
    return t('settings.audio.models.defaultPendingBadge');
  });

  let modelBadgeIsWarning = $derived(!hasExplicitModels && acousticAvailability.kind === 'none');

  let modelBadgeTitle = $derived.by(() => {
    if (hasExplicitModels || acousticAvailability.kind !== 'none') return undefined;
    return acousticAvailability.reason === 'load_failed'
      ? t('settings.audio.models.loadFailedWarning')
      : t('settings.audio.models.noneEnabledHelp');
  });

  // A selected model that the server no longer lists, or that is not loaded.
  // Unknown while the list loads or after a failed fetch, so no warning then.
  let needsModelAttention = $derived(
    !listFailed &&
      !listLoading &&
      selectedModels.some(id => {
        const model = availableModels.find(m => m.id === id);
        return !model || model.state === 'failed' || model.state === 'pending';
      })
  );

  const BADGE_CLASS = 'px-2 py-0.5 rounded text-xs font-semibold';

  // ----- Name -----

  let nameDraft = $state(untrack(() => source.name));
  let nameFocused = $state(false);
  let nameError = $state<string | null>(null);

  // Follows the saved name while the field is not being edited.
  $effect(() => {
    const saved = source.name;
    untrack(() => {
      if (!nameFocused) nameDraft = saved;
    });
  });

  function validateName(name: string): string | null {
    if (!name) return t('settings.audio.soundCards.errors.nameRequired');
    const lower = name.toLowerCase();
    if (sources.some((s, i) => i !== index && s.name.toLowerCase() === lower)) {
      return t('settings.audio.soundCards.errors.duplicateName');
    }
    return null;
  }

  function commitName() {
    const trimmed = nameDraft.trim();
    if (trimmed === source.name) {
      nameDraft = source.name;
      nameError = null;
      return;
    }
    const error = validateName(trimmed);
    nameError = error;
    if (error) return;
    nameDraft = trimmed;
    commit({ name: trimmed }, 'name');
  }

  function handleNameKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      commitName();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      nameDraft = source.name;
      nameError = null;
    }
  }

  let nameDescribedBy = $derived(
    [nameError ? nameErrorId : undefined, errorDescription('name')].filter(Boolean).join(' ') ||
      undefined
  );

  // ----- Capture (device and sample rate, applied together) -----

  let savedRate = $derived(source.sampleRate || DEFAULT_SAMPLE_RATE);
  let draftDevice = $state(untrack(() => source.device));
  let draftRate = $state(untrack(() => source.sampleRate || DEFAULT_SAMPLE_RATE));
  // Set by a change to the draft; the draft then no longer follows the saved values.
  let captureEdited = $state(false);
  let applyPending = $state(false);
  let sampleRateOptions = $state<Array<{ value: string; label: string }>>(DEFAULT_RATE_OPTIONS);
  let sampleRateVerified = $state(true);
  let sampleRateLoading = $state(false);
  // Plain (non-reactive) ref: the probe effect both reads (abort) and writes this
  // controller in its synchronous prefix. As $state that would make the effect
  // depend on it and rerun at once, aborting the new probe (issue #3593).
  let fetchController: AbortController | null = null;

  let captureDirty = $derived(draftDevice !== source.device || draftRate !== savedRate);
  let captureStatus = $derived(autoSaveStatus(field('capture')));
  let applying = $derived(applyPending && captureStatus.state === 'saving');

  $effect(() => {
    const device = source.device;
    const rate = source.sampleRate || DEFAULT_SAMPLE_RATE;
    untrack(() => {
      if (!captureEdited) {
        draftDevice = device;
        draftRate = rate;
      }
    });
  });

  // Ends an apply: a save hands the draft back to the saved values, a failure keeps it.
  $effect(() => {
    const state = captureStatus.state;
    untrack(() => {
      if (!applyPending || state === 'saving') return;
      applyPending = false;
      if (state === 'error') return;
      captureEdited = false;
      draftDevice = source.device;
      draftRate = source.sampleRate || DEFAULT_SAMPLE_RATE;
    });
  });

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
    draftDevice = value;
    captureEdited = true;
  }

  function setDraftRate(value: string | string[]) {
    if (typeof value !== 'string') return;
    draftRate = Number(value);
    captureEdited = true;
  }

  function cancelCapture() {
    captureEdited = false;
    draftDevice = source.device;
    draftRate = savedRate;
  }

  function applyCapture() {
    if (applying || disabled) return;
    applyPending = true;
    commit({ device: draftDevice, sampleRate: draftRate }, 'capture');
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
      // A newly picked device gets a rate it supports. The saved device keeps its
      // saved rate, so opening a card never creates a change to apply.
      if (draftDevice !== source.device) {
        draftRate = coerceSupportedRate(result.options, draftRate);
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
    // A disabled equalizer without filters is stored as undefined
    const next =
      updated.enabled || updated.filters.length > 0
        ? { enabled: updated.enabled, filters: updated.filters }
        : undefined;
    // Switching it or adding and removing filters is discrete; a filter value is continuous.
    const continuous =
      updated.enabled === equalizer.enabled && updated.filters.length === equalizer.filters.length;
    commit({ equalizer: next }, 'equalizer', continuous);
  }

  // ----- Quiet hours -----

  let quietHours = $derived<QuietHoursConfig>(source.quietHours ?? { ...defaultQuietHoursConfig });
  const QUIET_HOURS_DISCRETE_KEYS = ['enabled', 'mode', 'startEvent', 'endEvent'] as const;

  function commitQuietHours(updated: QuietHoursConfig) {
    // Times and offsets are typed or stepped, so they wait for a pause.
    const continuous = QUIET_HOURS_DISCRETE_KEYS.every(
      // eslint-disable-next-line security/detect-object-injection -- key is a fixed QuietHoursConfig key
      key => updated[key] === quietHours[key]
    );
    commit({ quietHours: updated }, 'quietHours', continuous);
  }

  // ----- Delete -----

  let showDeleteConfirm = $state(false);
  let deleteButton = $state<HTMLButtonElement>();
  let cancelDeleteButton = $state<HTMLButtonElement>();

  async function confirmDelete() {
    if (disabled) return;
    showDeleteConfirm = true;
    await tick();
    cancelDeleteButton?.focus();
  }

  async function cancelDelete() {
    showDeleteConfirm = false;
    await tick();
    deleteButton?.focus();
  }

  function executeDelete() {
    showDeleteConfirm = false;
    onDelete();
  }

  function handleDeleteKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      void cancelDelete();
    }
  }
</script>

{#snippet groupHeading(part: AudioSourceFieldPart, title: string, headingId: string)}
  <div class="flex min-h-6 items-center justify-between gap-3">
    <h4 id={headingId} class="text-sm font-semibold text-[var(--color-base-content)]">{title}</h4>
    <AutoSaveStatus field={field(part)} id={statusId(part)} />
  </div>
{/snippet}

<div
  class={cn(
    'relative rounded-lg border transition-colors duration-200',
    expanded
      ? 'border-[var(--border-200)] bg-[var(--color-base-100)] shadow-sm'
      : 'border-[var(--border-200)] bg-[var(--color-base-200)]'
  )}
>
  {#if showDeleteConfirm}
    <div
      role="alertdialog"
      aria-labelledby={deleteTitleId}
      aria-describedby={deleteDescId}
      tabindex="-1"
      onkeydown={handleDeleteKeydown}
      class="absolute inset-x-0 top-0 z-10 flex min-h-16 items-center rounded-lg bg-[var(--color-base-300)] px-4 py-3"
    >
      <div class="flex w-full items-center gap-3">
        <AlertCircle class="size-6 shrink-0 text-[var(--text-error)]" aria-hidden="true" />
        <span id={deleteTitleId} class="sr-only">{t('common.delete')}</span>
        <p id={deleteDescId} class="flex-1 text-sm font-medium text-[var(--color-base-content)]">
          {t('settings.audio.soundCards.deleteConfirm')}
          <span class="font-semibold">{source.name}</span>
        </p>
        <div class="flex shrink-0 gap-2">
          <button
            bind:this={cancelDeleteButton}
            type="button"
            class="inline-flex h-8 items-center justify-center rounded-lg px-3 text-sm font-medium transition-colors hover:bg-[var(--color-base-content)]/10"
            onclick={cancelDelete}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex h-8 items-center justify-center rounded-lg bg-[var(--color-error)] px-3 text-sm font-medium text-[var(--color-error-content)] transition-colors hover:bg-[var(--color-error-hover)]"
            onclick={executeDelete}
          >
            {t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Header: the button toggles the body; delete sits beside it -->
  <div class="flex items-center gap-2 p-3">
    <button
      type="button"
      class="flex min-w-0 flex-1 items-center gap-3 rounded-lg text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
      aria-expanded={expanded}
      aria-controls={bodyId}
      onclick={onToggleExpanded}
    >
      <ChevronRight
        class={cn(
          'size-4 shrink-0 text-muted transition-transform duration-200 motion-reduce:transition-none',
          expanded && 'rotate-90'
        )}
        aria-hidden="true"
      />
      <span
        class="flex size-10 shrink-0 items-center justify-center rounded-lg border border-[color-mix(in_srgb,var(--color-primary)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-primary)_15%,transparent)] text-[var(--color-primary)]"
      >
        <Mic class="size-5" aria-hidden="true" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block truncate text-sm font-semibold text-[var(--color-base-content)]">
          {source.name}
        </span>
        <span class="mt-0.5 block truncate text-xs text-muted">{deviceDisplayName}</span>
      </span>
    </button>

    <div class="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
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
      {#if needsModelAttention}
        <span
          class={cn(
            BADGE_CLASS,
            'inline-flex items-center gap-1 bg-[var(--color-warning)]/15 text-[var(--text-warning)]'
          )}
        >
          <AlertTriangle class="size-3" aria-hidden="true" />
          {t('settings.audio.models.warningBadge')}
        </span>
      {/if}
      <span
        class={cn(
          BADGE_CLASS,
          'max-w-64 truncate',
          modelBadgeIsWarning
            ? 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]'
            : 'bg-[var(--color-info)]/15 text-[var(--text-info)]'
        )}
        title={modelBadgeTitle ?? modelDisplayName}
      >
        {modelDisplayName}
      </span>
    </div>

    <button
      bind:this={deleteButton}
      type="button"
      class="inline-flex size-8 shrink-0 items-center justify-center rounded-lg transition-colors hover:bg-[var(--color-base-content)]/10 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      onclick={confirmDelete}
      aria-disabled={disabled ? 'true' : undefined}
      aria-label={t('common.delete')}
    >
      <Trash2 class="size-4" aria-hidden="true" />
    </button>
  </div>

  {#if expanded}
    <div
      id={bodyId}
      class="divide-y divide-[var(--border-100)] border-t border-[var(--border-100)] px-4"
      transition:slide={{ duration: 150 }}
    >
      <!-- Name and level -->
      <div class="grid grid-cols-1 gap-x-8 gap-y-4 py-4 md:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <div class="flex min-h-6 items-center justify-between gap-3">
            <label for={nameInputId} class="text-sm font-semibold text-[var(--color-base-content)]">
              {t('settings.audio.soundCards.nameLabel')}
              <span class="ml-0.5 text-[var(--text-error)]" aria-hidden="true">*</span>
            </label>
            <AutoSaveStatus field={field('name')} id={statusId('name')} />
          </div>
          <input
            id={nameInputId}
            type="text"
            class={cn('input input-sm w-full', nameError && 'input-error')}
            bind:value={nameDraft}
            maxlength={NAME_MAX_LENGTH}
            required
            aria-required="true"
            aria-invalid={nameError ? 'true' : undefined}
            aria-describedby={nameDescribedBy}
            readonly={disabled}
            onfocus={() => (nameFocused = true)}
            onblur={() => {
              nameFocused = false;
              commitName();
            }}
            oninput={() => (nameError = null)}
            onkeydown={handleNameKeydown}
          />
          {#if nameError}
            <p id={nameErrorId} class="text-xs text-[var(--text-error)]">{nameError}</p>
          {/if}
        </div>

        <div class="min-w-0 space-y-1.5">
          {@render groupHeading(
            'gain',
            t('settings.audio.soundCards.levelTitle'),
            `${cardId}-level`
          )}
          <InlineSlider
            label={t('settings.audio.soundCards.gainLabel')}
            value={source.gain}
            onUpdate={value => commit({ gain: value }, 'gain', true)}
            min={AUDIO_GAIN_MIN_DB}
            max={AUDIO_GAIN_MAX_DB}
            step={1}
            unit=" dB"
            {disabled}
          />
        </div>
      </div>

      <!-- Capture: drafts with a scoped apply -->
      <section class="space-y-3 py-4" aria-labelledby={captureHeadingId}>
        {@render groupHeading(
          'capture',
          t('settings.audio.soundCards.captureTitle'),
          captureHeadingId
        )}
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <SelectDropdown
            value={draftDevice}
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
              value={String(draftRate)}
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
            {#if draftRate > SHARED_ACCESS_MAX_RATE}
              <p class="mt-1 flex items-center gap-1 text-xs text-[var(--text-info)]">
                <Info class="size-3 shrink-0" aria-hidden="true" />
                {t('settings.audio.soundCards.sampleRateExclusive')}
              </p>
            {/if}
          </div>
        </div>
        {#if captureDirty}
          <div
            class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] px-3 py-2"
          >
            <p class="flex items-center gap-2 text-sm text-[var(--color-base-content)]">
              <RotateCcw class="size-4 shrink-0 text-muted" aria-hidden="true" />
              {t('settings.audio.soundCards.captureApplyHint')}
            </p>
            <div class="flex gap-2">
              <button
                type="button"
                class="btn btn-ghost btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                aria-disabled={applying ? 'true' : undefined}
                onclick={() => {
                  if (!applying) cancelCapture();
                }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-60"
                aria-disabled={applying || disabled ? 'true' : undefined}
                aria-describedby={errorDescription('capture')}
                onclick={applyCapture}
              >
                {applying
                  ? t('settings.audio.soundCards.applying')
                  : t('settings.audio.soundCards.applyCapture')}
              </button>
            </div>
          </div>
        {/if}
      </section>

      <!-- Classifiers -->
      <section class="space-y-3 py-4" aria-labelledby={modelsHeadingId}>
        {@render groupHeading('models', t('settings.audio.soundCards.modelLabel'), modelsHeadingId)}
        <ModelToggleList
          models={availableModels}
          {selectedModels}
          {defaultModels}
          sourceName={source.name}
          sourceSampleRate={draftRate}
          loading={listLoading}
          failed={listFailed}
          availability={acousticAvailability}
          labelledBy={modelsHeadingId}
          {disabled}
          onChange={models => commit({ models }, 'models')}
          onRetry={() => void refreshModels()}
        />
      </section>

      <!-- Audio filters (collapsed by default) -->
      <section class="py-4">
        <div class="flex min-h-6 items-center justify-between gap-3">
          <button
            type="button"
            class="flex items-center gap-2 rounded text-sm font-semibold text-[var(--color-base-content)] transition-colors hover:text-[var(--text-primary)]"
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
              <span
                class={cn(BADGE_CLASS, 'bg-[var(--color-primary)]/15 text-[var(--text-primary)]')}
              >
                {t('common.enabled')}
              </span>
            {/if}
          </div>
        </div>
        {#if showEqualizer}
          <div class="mt-3" transition:slide={{ duration: 150 }}>
            <AudioEqualizerSettings
              equalizerSettings={equalizer}
              {disabled}
              onUpdate={commitEqualizer}
              sampleRate={draftRate}
            />
          </div>
        {/if}
      </section>

      <!-- Quiet hours -->
      <section class="space-y-3 py-4" aria-labelledby="{cardId}-quiet">
        {@render groupHeading(
          'quietHours',
          t('settings.audio.quietHours.title'),
          `${cardId}-quiet`
        )}
        <QuietHoursEditor
          config={quietHours}
          onChange={commitQuietHours}
          {disabled}
          idPrefix="{cardId}-qh"
          enableLabel={t('settings.audio.quietHours.enableLabel')}
        />
      </section>
    </div>
  {/if}
</div>

<!--
  Sound Card Manager Component

  Purpose: Manage the sound card audio sources of a tab that saves with the
  page: source cards, an add form that appends the new source to the form,
  delete, a summary bar and empty state guidance.

  Every change goes out through onUpdateSources as the whole sources array; the
  page's Save Changes bar persists it. Rows carry a UI-only id (never persisted)
  that keys the cards and their expanded state. The manager remembers the
  identity (name and device of every row) of the list it last sent; a list with
  another identity (Reset, a reload) is foreign and gives every row a fresh id,
  so no card state lands on a different source.

  Features:
  - Source cards: one source opens by default, several start closed
  - Add new sources with name, device, gain, models, equalizer, quiet hours
  - Summary bar showing source count
  - Empty state with guidance
  - Duplicate name/device validation

  @component
-->
<script lang="ts">
  import { Plus, Mic, RefreshCw, ChevronDown, AlertTriangle, Info } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { tick, untrack } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { t } from '$lib/i18n';
  import { generateId } from '$lib/utils/uuid';
  import { loggers } from '$lib/utils/logger';
  import {
    fetchDeviceCapabilities as fetchCapabilities,
    coerceSupportedRate,
  } from '$lib/utils/audio/sampleRate';
  import { cn } from '$lib/utils/cn';
  import {
    deviceValue,
    deviceMatches,
    deviceLabel,
    type AudioDevice,
  } from '$lib/utils/audioDevices';
  import {
    getAvailableModels,
    watchModels,
    modelsLoading,
    modelsFailed,
    refreshModels,
  } from '$lib/stores/models.svelte';
  import { savedEqualizer } from './sourceFields';
  import { soundCardProblems } from './sourceRules';
  import {
    acousticModelAvailability,
    subscribeAcousticModels,
  } from '$lib/stores/acousticModels.svelte';
  import { modelLabel } from '$lib/utils/modelLabel';
  import { defaultModelSelection } from '$lib/utils/defaultModelSelection';
  import SoundCardCard from './SoundCardCard.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import TextInput from './TextInput.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import ModelToggleList from './ModelToggleList.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import type {
    AudioSourceConfig,
    EqualizerFilterType,
    QuietHoursConfig,
  } from '$lib/stores/settings';
  import {
    defaultQuietHoursConfig,
    AUDIO_GAIN_MIN_DB,
    AUDIO_GAIN_MAX_DB,
  } from '$lib/stores/settings';

  // Local EqualizerSettings type matching AudioEqualizerSettings component's interface
  interface LocalEqualizerSettings {
    enabled: boolean;
    filters: Array<{
      type: EqualizerFilterType;
      frequency: number;
      q?: number;
      width?: number;
      gain?: number;
      passes?: number;
    }>;
  }

  const logger = loggers.audio;
  const ROW_ID_PREFIX = 'source-row';

  const availableModels = $derived(getAvailableModels());
  const acousticAvailability = $derived(acousticModelAvailability());

  // Pre-selection for a source without an explicit list: the classifier's
  // default targets when known, nothing at N=0, the legacy BirdNET pick otherwise.
  function getDefaultModels(): string[] {
    return defaultModelSelection(acousticAvailability, availableModels);
  }

  $effect(() => {
    return watchModels();
  });

  $effect(() => {
    return subscribeAcousticModels();
  });

  const modelOptions = $derived(availableModels.map(m => ({ value: m.id, label: modelLabel(m) })));

  interface Props {
    sources: AudioSourceConfig[];
    /** The saved sources, so only problems the user introduced are reported. */
    savedSources?: AudioSourceConfig[];
    audioDevices: AudioDevice[];
    audioDevicesLoading: boolean;
    disabled?: boolean;
    /** Why the cards are disabled, shown beside each card's delete button. */
    disabledReason?: string;
    onUpdateSources: (_sources: AudioSourceConfig[]) => void;
    onRefreshDevices: () => void;
  }

  let {
    sources = [],
    savedSources = [],
    audioDevices,
    audioDevicesLoading,
    disabled = false,
    disabledReason,
    onUpdateSources,
    onRefreshDevices,
  }: Props = $props();

  // Add form state
  let showAddForm = $state(false);
  let newName = $state('');
  let newDevice = $state('');
  let newGain = $state(0);
  let newSampleRate = $state(48000);
  let newSampleRateOptions = $state<Array<{ value: string; label: string }>>([
    { value: '48000', label: '48 kHz' },
  ]);
  let newSampleRateVerified = $state(true);
  let newSampleRateLoading = $state(false);
  // Plain (non-reactive) ref: the probe effect both reads (abort) and writes this
  // controller in its synchronous prefix. As $state that read/write would register
  // the controller as a dependency of the effect and immediately re-run it, whose
  // cleanup then aborts the brand-new in-flight probe (issue #3593). It is only
  // used imperatively for abort(), never in markup, so it must not be reactive.
  let newFetchController: AbortController | null = null;
  let newModels = $state<string[]>([]);
  let newEqualizer = $state<LocalEqualizerSettings>({ enabled: false, filters: [] });
  let newQuietHours = $state<QuietHoursConfig>({ ...defaultQuietHoursConfig });
  let showNewEqualizer = $state(false);
  let nameError = $state<string | null>(null);
  let deviceError = $state<string | null>(null);

  // Device dropdown options. New selections persist the reboot-stable USB token
  // so they survive reboots (GH #3651); the label disambiguates identical device
  // names with the bus path only when needed. Filter out devices already
  // configured as a source by either identifier form.
  let deviceOptions = $derived(
    audioDevices
      .filter(d => !sources.some(s => deviceMatches(d, s.device)))
      .map(d => ({ value: deviceValue(d), label: deviceLabel(d, audioDevices) }))
  );

  // Clear form errors
  function clearErrors() {
    nameError = null;
    deviceError = null;
  }

  // Reset add form
  function resetAddForm() {
    newName = '';
    newDevice = '';
    newGain = 0;
    newSampleRate = 48000;
    newSampleRateOptions = [{ value: '48000', label: '48 kHz' }];
    newSampleRateVerified = true;
    prevNewDevice = '';
    newModels = getDefaultModels();
    newEqualizer = { enabled: false, filters: [] };
    newQuietHours = { ...defaultQuietHoursConfig };
    showNewEqualizer = false;
    clearErrors();
    showAddForm = false;
  }

  // Open add form with default model pre-selected
  function openAddForm() {
    if (disabled) return;
    newModels = getDefaultModels();
    showAddForm = true;
  }

  // ----- Rows -----

  function newRowIds(count: number): string[] {
    return Array.from({ length: count }, () => generateId(ROW_ID_PREFIX));
  }

  let rowIds = $state<string[]>(untrack(() => newRowIds(sources.length)));
  // One source opens by default, several start closed.
  const expandedRows = new SvelteSet<string>(untrack(() => (rowIds.length === 1 ? rowIds : [])));

  // A source is identified by its name and device, the fields a card's open
  // state and pending device pick are about; other edits keep the identity.
  function listIdentity(list: readonly AudioSourceConfig[]): string {
    return JSON.stringify(list.map(source => [source.name, source.device]));
  }

  // The identity of the list this manager last held or sent.
  let expectedIdentity = untrack(() => listIdentity(sources));

  /** Sends a list to the parent and remembers its identity as the expected one. */
  function sendSources(list: AudioSourceConfig[]) {
    expectedIdentity = listIdentity(list);
    onUpdateSources(list);
  }

  // A list this manager did not produce (Reset, a reload) gives every row a
  // fresh id, so the cards are recreated and no open confirmation, pending
  // device pick or open state lands on a different source.
  $effect.pre(() => {
    const identity = listIdentity(sources);
    untrack(() => {
      if (identity === expectedIdentity) return;
      expectedIdentity = identity;
      rowIds = newRowIds(sources.length);
      expandedRows.clear();
      if (sources.length === 1) expandedRows.add(rowIds[0]);
    });
  });

  let problems = $derived(soundCardProblems(sources, savedSources));
  let rows = $derived(
    sources.map((source, index) => ({ source, index, id: rowIds.at(index) ?? `${index}` }))
  );

  function toggleRow(id: string) {
    if (expandedRows.has(id)) expandedRows.delete(id);
    else expandedRows.add(id);
  }

  let addButton = $state<HTMLButtonElement>();

  // ----- Add -----

  function addSource() {
    if (disabled) return;
    clearErrors();

    const trimmedName = newName.trim();
    const newSource: AudioSourceConfig = {
      name: trimmedName,
      device: newDevice,
      sampleRate: newSampleRate,
      gain: newGain,
      models: newModels,
      equalizer: savedEqualizer(newEqualizer),
      quietHours: newQuietHours,
    };

    // The new row against the current rows only: the saved list is not a reason to accept it.
    const problem = soundCardProblems([...sources, newSource], []).at(-1);
    if (problem?.name) {
      nameError =
        problem.name === 'required'
          ? t('settings.audio.soundCards.errors.nameRequired')
          : t('settings.audio.soundCards.errors.duplicateName');
      return;
    }
    if (problem?.device) {
      deviceError =
        problem.device === 'required'
          ? t('settings.audio.soundCards.errors.deviceRequired')
          : t('settings.audio.soundCards.errors.duplicateDevice');
      return;
    }

    logger.debug('Adding sound card source', {
      component: 'SoundCardManager',
      action: 'addSource',
    });
    const rowId = generateId(ROW_ID_PREFIX);
    rowIds = [...rowIds, rowId];
    if (sources.length === 0) expandedRows.add(rowId);
    sendSources([...sources, newSource]);
    resetAddForm();
  }

  // ----- Change and delete -----

  function changeSource(index: number, updated: AudioSourceConfig) {
    if (index < 0 || index >= sources.length) return;
    sendSources(sources.map((source, i) => (i === index ? updated : source)));
  }

  async function deleteSource(index: number) {
    const id = rowIds.at(index);
    rowIds = rowIds.filter((_, i) => i !== index);
    if (id !== undefined) expandedRows.delete(id);
    sendSources(sources.filter((_, i) => i !== index));
    // The deleted card took focus with it; land on the add button instead.
    await tick();
    addButton?.focus();
  }

  function handleNewEqualizerUpdate(updated: LocalEqualizerSettings) {
    newEqualizer = { ...updated };
  }

  async function fetchNewDeviceCapabilities(deviceId: string) {
    if (!deviceId) return;
    newFetchController?.abort();
    const controller = new AbortController();
    newFetchController = controller;
    newSampleRateLoading = true;
    // Drop the previous device's probed rates so a slower probe cannot leave
    // stale, unverified options on screen while the new one is in flight.
    newSampleRateOptions = [{ value: '48000', label: '48 kHz' }];
    newSampleRateVerified = true;
    try {
      const result = await fetchCapabilities(deviceId, controller.signal);
      // Ignore a superseded probe: a newer device selection has replaced this
      // controller, so applying these results (or clearing the loading flag)
      // would clobber the newer probe's state.
      if (newFetchController !== controller) return;
      newSampleRateOptions = result.options;
      newSampleRateVerified = result.verified;
      // Coerce the selection to a rate the new device actually supports so an
      // unsupported rate is never persisted.
      newSampleRate = coerceSupportedRate(result.options, newSampleRate);
    } catch {
      // Only AbortError reaches here (utility handles all other failures internally)
    } finally {
      if (newFetchController === controller) {
        newSampleRateLoading = false;
      }
    }
  }

  let prevNewDevice = '';
  $effect(() => {
    if (newDevice && newDevice !== prevNewDevice) {
      prevNewDevice = newDevice;
      fetchNewDeviceCapabilities(newDevice);
    }
    // Capture the controller this run started so the cleanup only aborts that
    // probe. A later re-run for an unrelated reason must not abort a probe it
    // did not start (issue #3593).
    const controllerToAbort = newFetchController;
    return () => {
      controllerToAbort?.abort();
    };
  });
</script>

<div class="space-y-4">
  <!-- Summary Bar -->
  {#if sources.length > 0}
    <div class="flex items-center justify-between p-3 bg-[var(--color-base-200)] rounded-lg">
      <div class="flex items-center gap-2">
        <Mic class="size-4 text-[var(--color-base-content)]/70" />
        <span class="text-sm font-medium">
          {t('settings.audio.soundCards.summary', { count: sources.length })}
        </span>
      </div>

      <button
        type="button"
        class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={onRefreshDevices}
        disabled={audioDevicesLoading || disabled}
      >
        <RefreshCw class={cn('size-4', audioDevicesLoading && 'animate-spin')} />
        {t('settings.audio.audioCapture.refreshDevices')}
      </button>
    </div>
  {/if}

  <!-- Source Cards -->
  {#if sources.length === 0 && !showAddForm}
    <EmptyState
      icon={Mic}
      title={t('settings.audio.soundCards.emptyState.title')}
      description={t('settings.audio.soundCards.emptyState.description')}
      hints={[
        t('settings.audio.soundCards.emptyState.hints.device'),
        t('settings.audio.soundCards.emptyState.hints.multiple'),
        t('settings.audio.soundCards.emptyState.hints.model'),
      ]}
      hintsTitle={t('settings.audio.soundCards.emptyState.hintsTitle')}
      primaryAction={{
        label: t('settings.audio.soundCards.addSource'),
        icon: Plus,
        onclick: () => {
          openAddForm();
        },
      }}
    />
  {:else}
    <div class="space-y-3">
      {#each rows as row (row.id)}
        <SoundCardCard
          source={row.source}
          {sources}
          {audioDevices}
          {modelOptions}
          {availableModels}
          {disabled}
          {disabledReason}
          expanded={expandedRows.has(row.id)}
          onToggleExpanded={() => toggleRow(row.id)}
          problem={problems.at(row.index)}
          onChange={updated => changeSource(row.index, updated)}
          onDelete={() => void deleteSource(row.index)}
        />
      {/each}
    </div>

    <!-- Add Source Form -->
    {#if showAddForm}
      <div
        class="rounded-lg overflow-hidden bg-[var(--color-base-200)] border border-[var(--color-primary)]"
      >
        <div class="p-6">
          <h3 class="text-base font-semibold">
            {t('settings.audio.soundCards.addSource')}
          </h3>

          <div class="space-y-4 mt-4">
            <!-- Source Name -->
            <div>
              <TextInput
                id="new-soundcard-name"
                bind:value={newName}
                label={t('settings.audio.soundCards.nameLabel')}
                placeholder={t('settings.audio.soundCards.namePlaceholder')}
                helpText={nameError ? undefined : t('settings.audio.soundCards.nameHelp')}
                error={nameError ?? undefined}
                required
                {disabled}
              />
            </div>

            <!-- Device Selection -->
            <div>
              <SelectDropdown
                value={newDevice}
                label={t('settings.audio.soundCards.deviceLabel')}
                placeholder={t('settings.audio.soundCards.devicePlaceholder')}
                options={deviceOptions}
                required
                {disabled}
                onChange={value => (newDevice = value as string)}
                groupBy={false}
                menuSize="sm"
              />
              {#if deviceError}
                <p role="alert" class="text-xs text-[var(--text-error)] mt-1">
                  {deviceError}
                </p>
              {/if}
            </div>

            <!-- Sample Rate -->
            <div>
              <SelectDropdown
                value={String(newSampleRate)}
                label={t('settings.audio.soundCards.sampleRateLabel')}
                options={newSampleRateOptions}
                {disabled}
                onChange={value => (newSampleRate = Number(value))}
                groupBy={false}
                menuSize="sm"
              />
              {#if !newSampleRateVerified && !newSampleRateLoading}
                <p class="flex items-center gap-1 text-xs text-[var(--text-warning)] mt-1">
                  <AlertTriangle class="size-3" aria-hidden="true" />
                  {t('settings.audio.soundCards.sampleRateUnverified')}
                </p>
              {/if}
              {#if newSampleRateLoading}
                <p class="text-xs text-muted mt-1">
                  {t('settings.audio.soundCards.sampleRateProbing')}
                </p>
              {/if}
              {#if newSampleRate > 48000}
                <p class="flex items-center gap-1 text-xs text-[var(--text-info)] mt-1">
                  <Info class="size-3 shrink-0" aria-hidden="true" />
                  {t('settings.audio.soundCards.sampleRateExclusive')}
                </p>
              {/if}
            </div>

            <!-- Gain -->
            <InlineSlider
              label={t('settings.audio.soundCards.gainLabel')}
              value={newGain}
              onUpdate={value => (newGain = value)}
              min={AUDIO_GAIN_MIN_DB}
              max={AUDIO_GAIN_MAX_DB}
              step={1}
              unit=" dB"
              {disabled}
            />

            <!-- Model Selection -->
            <div class="space-y-2">
              <h4 id="new-soundcard-models" class="text-sm font-semibold">
                {t('settings.audio.soundCards.modelLabel')}
              </h4>
              <ModelToggleList
                models={availableModels}
                selectedModels={newModels}
                defaultModels={getDefaultModels()}
                sourceName={newName.trim()}
                sourceSampleRate={newSampleRate}
                loading={modelsLoading()}
                failed={modelsFailed()}
                availability={acousticAvailability}
                labelledBy="new-soundcard-models"
                {disabled}
                onChange={models => (newModels = models)}
                onRetry={() => void refreshModels()}
              />
            </div>

            <!-- Equalizer (expandable) -->
            <div>
              <button
                type="button"
                class="flex items-center gap-2 text-sm font-medium text-[var(--color-base-content)] hover:text-[var(--text-primary)] transition-colors"
                aria-expanded={showNewEqualizer}
                onclick={() => (showNewEqualizer = !showNewEqualizer)}
              >
                <ChevronDown
                  class={cn(
                    'size-4 transition-transform duration-200',
                    showNewEqualizer && 'rotate-180'
                  )}
                />
                {t('settings.audio.audioFilters.title')}
              </button>
              {#if showNewEqualizer}
                <div class="mt-3" transition:slide={{ duration: 200 }}>
                  <AudioEqualizerSettings
                    equalizerSettings={newEqualizer}
                    {disabled}
                    onUpdate={handleNewEqualizerUpdate}
                    sampleRate={newSampleRate}
                  />
                </div>
              {/if}
            </div>

            <!-- Quiet Hours -->
            <QuietHoursEditor
              config={newQuietHours}
              onChange={qh => (newQuietHours = qh)}
              {disabled}
              idPrefix="new-soundcard-qh"
            />

            <!-- Action Buttons -->
            <div class="flex flex-wrap items-center justify-end gap-2 pt-2">
              <button
                type="button"
                class="btn btn-ghost btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                onclick={resetAddForm}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm gap-1.5 aria-disabled:cursor-not-allowed aria-disabled:opacity-60"
                aria-disabled={disabled ? 'true' : undefined}
                onclick={addSource}
              >
                <Plus class="size-4" aria-hidden="true" />
                {t('settings.audio.soundCards.addSource')}
              </button>
            </div>
          </div>
        </div>
      </div>
    {:else}
      <!-- Add Source Button -->
      <button
        bind:this={addButton}
        type="button"
        class="w-full inline-flex items-center justify-center gap-2 h-8 px-3 text-sm rounded-lg border border-dashed border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-content)]/5 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={openAddForm}
        disabled={disabled || deviceOptions.length === 0}
      >
        <Plus class="size-4" />
        {t('settings.audio.soundCards.addSource')}
      </button>
    {/if}
  {/if}
</div>

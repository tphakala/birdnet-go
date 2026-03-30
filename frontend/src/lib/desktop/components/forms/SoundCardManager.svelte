<!--
  Sound Card Manager Component

  Purpose: Manage multiple sound card audio sources with add/edit/delete
  functionality, summary bar, and empty state guidance.

  Features:
  - Display source cards with device and model info
  - Add new sources with name, device, gain, model, equalizer, quiet hours
  - Summary bar showing source count
  - Empty state with guidance
  - Duplicate name/device validation

  @component
-->
<script lang="ts">
  import { Plus, Volume2, RefreshCw, ChevronDown } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { cn } from '$lib/utils/cn';
  import SoundCardCard from './SoundCardCard.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import TextInput from './TextInput.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import type {
    AudioSourceConfig,
    EqualizerFilterType,
    QuietHoursConfig,
  } from '$lib/stores/settings';
  import { defaultQuietHoursConfig } from '$lib/stores/settings';

  // Local EqualizerSettings type matching AudioEqualizerSettings component's interface
  // where filter.id is optional (assigned on save)
  interface LocalEqualizerSettings {
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
  }

  const logger = loggers.audio;

  // Fetch available models from backend API
  interface BackendModel {
    id: string;
    name: string;
  }

  let availableModels = $state<BackendModel[]>([]);

  $effect(() => {
    const controller = new AbortController();

    untrack(() => {
      api
        .get<BackendModel[]>('/api/v2/models', { signal: controller.signal })
        .then(data => {
          if (Array.isArray(data)) {
            availableModels = data;
          } else {
            logger.warn('Fetched models response is not an array', {
              component: 'SoundCardManager',
            });
          }
        })
        .catch((err: unknown) => {
          if (err instanceof Error && err.name !== 'AbortError') {
            logger.error('Failed to fetch models', err, {
              component: 'SoundCardManager',
              action: 'fetchModels',
            });
          }
        });
    });

    return () => controller.abort();
  });

  // Model options — default entry + dynamically loaded models
  const modelOptions = $derived([
    { value: '', label: t('settings.audio.soundCards.models.birdnetDefault') },
    ...availableModels.map(m => ({ value: m.id, label: m.name })),
  ]);

  interface Props {
    sources: AudioSourceConfig[];
    audioDevices: Array<{ index: number; name: string; id: string }>;
    audioDevicesLoading: boolean;
    disabled?: boolean;
    onUpdateSources: (_sources: AudioSourceConfig[]) => void;
    onRefreshDevices: () => void;
  }

  let {
    sources = [],
    audioDevices,
    audioDevicesLoading,
    disabled = false,
    onUpdateSources,
    onRefreshDevices,
  }: Props = $props();

  // Add form state
  let showAddForm = $state(false);
  let newName = $state('');
  let newDevice = $state('');
  let newGain = $state(0);
  let newModel = $state('');
  let newEqualizer = $state<LocalEqualizerSettings>({ enabled: false, filters: [] });
  let newQuietHours = $state<QuietHoursConfig>({ ...defaultQuietHoursConfig });
  let showNewEqualizer = $state(false);
  let nameError = $state<string | null>(null);
  let deviceError = $state<string | null>(null);

  // Device dropdown options
  let deviceOptions = $derived(audioDevices.map(d => ({ value: d.id, label: d.name })));

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
    newModel = '';
    newEqualizer = { enabled: false, filters: [] };
    newQuietHours = { ...defaultQuietHoursConfig };
    showNewEqualizer = false;
    clearErrors();
    showAddForm = false;
  }

  // Add new source
  function addSource() {
    clearErrors();

    const trimmedName = newName.trim();

    // Validate name
    if (!trimmedName) {
      nameError = t('settings.audio.soundCards.errors.nameRequired');
      return;
    }

    // Check duplicate name (case-insensitive)
    if (sources.some(s => s.name.toLowerCase() === trimmedName.toLowerCase())) {
      nameError = t('settings.audio.soundCards.errors.duplicateName');
      return;
    }

    // Validate device
    if (!newDevice) {
      deviceError = t('settings.audio.soundCards.errors.deviceRequired');
      return;
    }

    // Check duplicate device
    if (sources.some(s => s.device === newDevice)) {
      deviceError = t('settings.audio.soundCards.errors.duplicateDevice');
      return;
    }

    // Transform equalizer filters to ensure all have an id (required by store type)
    const transformedEqualizer =
      newEqualizer.enabled || newEqualizer.filters.length > 0
        ? {
            enabled: newEqualizer.enabled,
            filters: newEqualizer.filters.map(f => ({
              ...f,
              id: f.id || crypto.randomUUID(),
            })),
          }
        : undefined;

    const newSource: AudioSourceConfig = {
      name: trimmedName,
      device: newDevice,
      gain: newGain,
      model: newModel,
      equalizer: transformedEqualizer,
      quietHours: newQuietHours,
    };

    onUpdateSources([...sources, newSource]);
    resetAddForm();
  }

  // Update source — returns boolean for success
  function updateSource(index: number, updatedSource: AudioSourceConfig): boolean {
    const updatedSources = [...sources];
    if (index >= 0 && index < updatedSources.length) {
      // Check duplicate name (excluding current, case-insensitive)
      const nameLower = updatedSource.name.toLowerCase();
      if (updatedSources.some((s, i) => i !== index && s.name.toLowerCase() === nameLower)) {
        logger.warn('Duplicate sound card source name', null, {
          component: 'SoundCardManager',
          action: 'updateSource',
        });
        toastActions.error(t('settings.audio.soundCards.errors.duplicateName'));
        return false;
      }

      // Check duplicate device (excluding current)
      if (
        updatedSource.device !== updatedSources.at(index)?.device &&
        updatedSources.some((s, i) => i !== index && s.device === updatedSource.device)
      ) {
        logger.warn('Duplicate sound card device', null, {
          component: 'SoundCardManager',
          action: 'updateSource',
        });
        toastActions.error(t('settings.audio.soundCards.errors.duplicateDevice'));
        return false;
      }

      updatedSources.splice(index, 1, updatedSource);
      onUpdateSources(updatedSources);
      return true;
    }
    return false;
  }

  // Delete source
  function deleteSource(index: number) {
    const updatedSources = sources.filter((_, i) => i !== index);
    onUpdateSources(updatedSources);
  }

  function handleNewEqualizerUpdate(updated: LocalEqualizerSettings) {
    newEqualizer = {
      enabled: updated.enabled,
      filters: updated.filters.map(filter => ({
        ...filter,
        id: filter.id || crypto.randomUUID(),
      })),
    };
  }
</script>

<div class="space-y-4">
  <!-- Summary Bar -->
  {#if sources.length > 0}
    <div class="flex items-center justify-between p-3 bg-[var(--color-base-200)] rounded-lg">
      <div class="flex items-center gap-2">
        <Volume2 class="size-4 text-[var(--color-base-content)]/70" />
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
      icon={Volume2}
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
          if (!disabled) showAddForm = true;
        },
      }}
    />
  {:else}
    <div class="space-y-3">
      {#each sources as source, index (`${source.device}_${index}`)}
        <SoundCardCard
          {source}
          {index}
          {audioDevices}
          {modelOptions}
          {disabled}
          onUpdate={updatedSource => updateSource(index, updatedSource)}
          onDelete={() => deleteSource(index)}
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
                {disabled}
              />
              {#if nameError}
                <p
                  role="alert"
                  aria-live="assertive"
                  class="text-xs text-[var(--color-error)] -mt-2"
                >
                  {nameError}
                </p>
              {/if}
            </div>

            <!-- Device Selection -->
            <div>
              <SelectDropdown
                value={newDevice}
                label={t('settings.audio.soundCards.deviceLabel')}
                placeholder={t('settings.audio.soundCards.devicePlaceholder')}
                options={deviceOptions}
                {disabled}
                onChange={value => (newDevice = value as string)}
                groupBy={false}
                menuSize="sm"
              />
              {#if deviceError}
                <p
                  role="alert"
                  aria-live="assertive"
                  class="text-xs text-[var(--color-error)] mt-1"
                >
                  {deviceError}
                </p>
              {/if}
            </div>

            <!-- Gain and Model -->
            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <InlineSlider
                label={t('settings.audio.soundCards.gainLabel')}
                value={newGain}
                onUpdate={value => (newGain = value)}
                min={-40}
                max={40}
                step={1}
                unit=" dB"
                {disabled}
              />

              <SelectDropdown
                value={newModel}
                label={t('settings.audio.soundCards.modelLabel')}
                options={modelOptions}
                {disabled}
                onChange={value => (newModel = value as string)}
                groupBy={false}
                menuSize="sm"
              />
            </div>

            <!-- Equalizer (expandable) -->
            <div>
              <button
                type="button"
                class="flex items-center gap-2 text-sm font-medium text-[var(--color-base-content)] hover:text-[var(--color-primary)] transition-colors"
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
            <div class="flex gap-2 justify-end pt-2">
              <button
                type="button"
                class="inline-flex items-center justify-center gap-2 px-3 py-1.5 text-sm font-medium rounded-md cursor-pointer transition-all bg-transparent text-[var(--color-base-content)] hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={resetAddForm}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                class="inline-flex items-center justify-center gap-2 px-3 py-1.5 text-sm font-medium rounded-md cursor-pointer transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={addSource}
                disabled={!newName.trim() || !newDevice || disabled}
              >
                <Plus class="size-4" />
                {t('settings.audio.soundCards.addSource')}
              </button>
            </div>
          </div>
        </div>
      </div>
    {:else}
      <!-- Add Source Button -->
      <button
        type="button"
        class="w-full inline-flex items-center justify-center gap-2 h-8 px-3 text-sm rounded-lg border border-dashed border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-content)]/5 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={() => (showAddForm = true)}
        {disabled}
      >
        <Plus class="size-4" />
        {t('settings.audio.soundCards.addSource')}
      </button>
    {/if}
  {/if}
</div>

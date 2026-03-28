<!--
  Sound Card Card Component

  Purpose: Display and manage a single sound card audio source with
  device selection, gain control, model selection, equalizer, and quiet hours.

  Features:
  - Card with view/edit modes
  - Device dropdown from available audio devices
  - Gain slider (-40 to +40 dB)
  - Model selector (BirdNET, Perch v2, Bat)
  - Per-source equalizer (expandable, reuses AudioEqualizerSettings)
  - Per-source quiet hours (reuses QuietHoursEditor)
  - Delete confirmation overlay
  - Keyboard shortcuts (Enter to save, Escape to cancel)

  @component
-->
<script lang="ts">
  import {
    Settings,
    Trash2,
    Check,
    X,
    AlertCircle,
    Volume2,
    Moon,
    ChevronDown,
  } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import SelectDropdown from './SelectDropdown.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
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

  interface Props {
    source: AudioSourceConfig;
    index: number;
    audioDevices: Array<{ index: number; name: string; id: string }>;
    disabled?: boolean;
    onUpdate: (_source: AudioSourceConfig) => boolean;
    onDelete: () => void;
  }

  let { source, index, audioDevices, disabled = false, onUpdate, onDelete }: Props = $props();

  // Model options — use $derived to re-translate on locale change
  const modelOptions = $derived([
    { value: '', label: t('settings.audio.soundCards.models.birdnetDefault') },
    { value: 'birdnet', label: t('settings.audio.soundCards.models.birdnet') },
    { value: 'perch_v2', label: t('settings.audio.soundCards.models.perchV2') },
    { value: 'bat', label: t('settings.audio.soundCards.models.bat') },
  ]);

  // Local editing state
  let isEditing = $state(false);
  let editName = $state('');
  let editDevice = $state('');
  let editGain = $state(0);
  let editModel = $state('');
  let editEqualizer = $state<LocalEqualizerSettings>({ enabled: false, filters: [] });
  let editQuietHours = $state<QuietHoursConfig>({ ...defaultQuietHoursConfig });
  let showDeleteConfirm = $state(false);
  let showEqualizer = $state(false);

  // Device display name lookup
  let deviceDisplayName = $derived(
    audioDevices.find(d => d.id === source.device)?.name ?? source.device
  );

  // Model display name
  let modelDisplayName = $derived(
    modelOptions.find(m => m.value === source.model)?.label ??
      t('settings.audio.soundCards.models.birdnetDefault')
  );

  // Device dropdown options
  let deviceOptions = $derived(audioDevices.map(d => ({ value: d.id, label: d.name })));

  // Edit mode functions
  function startEdit() {
    editName = source.name;
    editDevice = source.device;
    editGain = source.gain;
    editModel = source.model;
    editEqualizer = source.equalizer
      ? { ...source.equalizer, filters: [...source.equalizer.filters] }
      : { enabled: false, filters: [] };
    editQuietHours = source.quietHours ? { ...source.quietHours } : { ...defaultQuietHoursConfig };
    showEqualizer = false;
    isEditing = true;
  }

  function cancelEdit() {
    isEditing = false;
  }

  function saveEdit() {
    const trimmedName = editName.trim();
    if (!trimmedName || !editDevice) return;

    // Transform equalizer filters to ensure all have an id (required by store type)
    const transformedEqualizer =
      editEqualizer.enabled || editEqualizer.filters.length > 0
        ? {
            enabled: editEqualizer.enabled,
            filters: editEqualizer.filters.map(f => ({
              ...f,
              id: f.id || crypto.randomUUID(),
            })),
          }
        : undefined;

    const updated: AudioSourceConfig = {
      name: trimmedName,
      device: editDevice,
      gain: editGain,
      model: editModel,
      equalizer: transformedEqualizer,
      quietHours: editQuietHours,
    };

    const success = onUpdate(updated);
    if (success) {
      isEditing = false;
    }
  }

  function confirmDelete() {
    showDeleteConfirm = true;
  }

  function cancelDelete() {
    showDeleteConfirm = false;
  }

  function executeDelete() {
    showDeleteConfirm = false;
    onDelete();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
    }
  }

  function handleEqualizerUpdate(updated: LocalEqualizerSettings) {
    editEqualizer = updated;
  }
</script>

<div
  class={cn(
    'relative rounded-lg border transition-all duration-200',
    isEditing
      ? 'border-[var(--color-primary)]/50 bg-[var(--color-base-100)] shadow-md'
      : 'border-[var(--border-200)] bg-[var(--color-base-200)]',
    disabled && 'opacity-60 pointer-events-none'
  )}
>
  {#if showDeleteConfirm}
    <!-- Delete Confirmation Overlay -->
    <div
      role="alertdialog"
      aria-labelledby="soundcard-delete-title-{index}"
      aria-describedby="soundcard-delete-desc-{index}"
      class="absolute inset-0 z-10 flex items-center rounded-lg bg-[var(--color-base-300)]/95 backdrop-blur-sm px-4"
    >
      <div class="flex items-center gap-3 w-full">
        <AlertCircle class="size-6 text-[var(--color-error)] flex-shrink-0" />
        <span id="soundcard-delete-title-{index}" class="sr-only">{t('common.delete')}</span>
        <p
          id="soundcard-delete-desc-{index}"
          class="text-sm font-medium text-[var(--color-base-content)] flex-1"
        >
          {t('settings.audio.soundCards.deleteConfirm')}
        </p>
        <div class="flex gap-2 flex-shrink-0">
          <button
            type="button"
            class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors"
            onclick={cancelDelete}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:opacity-90 transition-colors"
            onclick={executeDelete}
          >
            {t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <div class="p-3">
    {#if isEditing}
      <!-- Edit Mode -->
      <div class="space-y-4">
        <!-- Name Input -->
        <div>
          <label class="block py-1" for="soundcard-name-{index}">
            <span class="text-xs font-medium text-[var(--color-base-content)]">
              {t('settings.audio.soundCards.nameLabel')}
            </span>
          </label>
          <input
            id="soundcard-name-{index}"
            type="text"
            bind:value={editName}
            onkeydown={handleKeydown}
            class="w-full h-9 px-3 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
            placeholder={t('settings.audio.soundCards.namePlaceholder')}
          />
        </div>

        <!-- Device Selection -->
        <SelectDropdown
          value={editDevice}
          label={t('settings.audio.soundCards.deviceLabel')}
          options={deviceOptions}
          onChange={value => (editDevice = value as string)}
          groupBy={false}
          menuSize="sm"
        />

        <!-- Gain and Model Row -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <InlineSlider
            label={t('settings.audio.soundCards.gainLabel')}
            value={editGain}
            onUpdate={value => (editGain = value)}
            min={-40}
            max={40}
            step={1}
            unit=" dB"
            {disabled}
          />

          <SelectDropdown
            value={editModel}
            label={t('settings.audio.soundCards.modelLabel')}
            options={modelOptions}
            onChange={value => (editModel = value as string)}
            groupBy={false}
            menuSize="sm"
          />
        </div>

        <!-- Equalizer (expandable) -->
        <div>
          <button
            type="button"
            class="flex items-center gap-2 text-sm font-medium text-[var(--color-base-content)] hover:text-[var(--color-primary)] transition-colors"
            onclick={() => (showEqualizer = !showEqualizer)}
          >
            <ChevronDown
              class={cn('size-4 transition-transform duration-200', showEqualizer && 'rotate-180')}
            />
            {t('settings.audio.audioFilters.title')}
            {#if editEqualizer.enabled}
              <span
                class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-primary)]/15 text-[var(--color-primary)]"
              >
                {t('common.enabled')}
              </span>
            {/if}
          </button>
          {#if showEqualizer}
            <div class="mt-3" transition:slide={{ duration: 200 }}>
              <AudioEqualizerSettings
                equalizerSettings={editEqualizer}
                {disabled}
                onUpdate={handleEqualizerUpdate}
              />
            </div>
          {/if}
        </div>

        <!-- Quiet Hours -->
        <QuietHoursEditor
          config={editQuietHours}
          onChange={qh => (editQuietHours = qh)}
          {disabled}
          idPrefix="soundcard-qh-{index}"
        />

        <!-- Action Buttons -->
        <div class="flex justify-end gap-2 pt-2 border-t border-[var(--border-200)]">
          <button
            type="button"
            class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors"
            onclick={cancelEdit}
          >
            <X class="size-4" />
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={saveEdit}
            disabled={!editName.trim() || !editDevice}
          >
            <Check class="size-4" />
            {t('common.save')}
          </button>
        </div>
      </div>
    {:else}
      <!-- View Mode -->
      <div class="flex items-center gap-3">
        <!-- Sound Card Icon -->
        <div
          class="flex-shrink-0 size-10 rounded-lg flex items-center justify-center border bg-[color-mix(in_srgb,var(--color-primary)_15%,transparent)] text-[var(--color-primary)] border-[color-mix(in_srgb,var(--color-primary)_25%,transparent)]"
        >
          <Volume2 class="size-5" />
        </div>

        <!-- Source Info -->
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="text-sm font-semibold text-[var(--color-base-content)]">
              {source.name}
            </span>
            {#if source.quietHours?.enabled}
              <span
                class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-base-300)] text-[var(--color-base-content)] opacity-70"
                title={t('settings.audio.quietHours.enabledBadge')}
              >
                <Moon class="size-3" />
                {t('settings.audio.quietHours.badge')}
              </span>
            {/if}
          </div>

          <p class="text-xs text-[var(--color-base-content)] opacity-70 mt-0.5">
            {deviceDisplayName}
          </p>
        </div>

        <!-- Right Side: Badges + Actions -->
        <div class="flex-shrink-0 flex items-center gap-2">
          <!-- Info Badges -->
          <div class="hidden sm:flex items-center gap-1.5">
            {#if source.gain !== 0}
              <span
                class="px-2 py-0.5 rounded text-xs font-semibold bg-[var(--color-warning)]/15 text-[var(--color-warning)]"
              >
                {source.gain > 0 ? '+' : ''}{source.gain} dB
              </span>
            {/if}
            <span
              class="px-2 py-0.5 rounded text-xs font-semibold bg-[var(--color-info)]/15 text-[var(--color-info)]"
            >
              {modelDisplayName}
            </span>
          </div>

          <!-- Action Buttons -->
          <div class="flex items-center gap-0.5">
            <button
              type="button"
              class="inline-flex items-center justify-center size-8 rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={startEdit}
              {disabled}
              aria-label={t('common.edit')}
            >
              <Settings class="size-4" />
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center size-8 rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              onclick={confirmDelete}
              {disabled}
              aria-label={t('common.delete')}
            >
              <Trash2 class="size-4" />
            </button>
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>

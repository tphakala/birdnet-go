<!--
  AudioToolbar.svelte - Persistent processing toolbar below spectrogram

  Provides: selection playback controls, gain/denoise/normalize processing,
  and export functionality. Inspired by iZotope RX toolbar layout.
-->
<script lang="ts">
  import {
    Play,
    Pause,
    SkipBack,
    X,
    Download,
    Volume2,
    AudioWaveform,
    Loader2,
    ChevronDown,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    // Selection state
    selectionStart: number | null;
    selectionEnd: number | null;
    hasSelection: boolean;

    // Processing state
    gainDb: number;
    denoise: string;
    normalize: boolean;
    isProcessing: boolean;
    isPlayingSelection: boolean;

    // Export state
    isExtracting: boolean;
    extractionError: string | null;

    // Callbacks
    onPlaySelection: () => void;
    onStopSelection: () => void;
    onSkipToSelection: () => void;
    onClearSelection: () => void;
    onGainChange: (_db: number) => void;
    onDenoiseChange: (_preset: string) => void;
    onNormalizeToggle: () => void;
    onExport: (_format: string) => void;
  }

  let {
    selectionStart,
    selectionEnd,
    hasSelection,
    gainDb = 0,
    denoise = '',
    normalize = false,
    isProcessing = false,
    isPlayingSelection = false,
    isExtracting = false,
    extractionError = null,
    onPlaySelection,
    onStopSelection,
    onSkipToSelection,
    onClearSelection,
    onGainChange,
    onDenoiseChange,
    onNormalizeToggle,
    onExport,
  }: Props = $props();

  let showExportMenu = $state(false);

  const exportFormats = [
    { id: 'wav', label: 'WAV' },
    { id: 'flac', label: 'FLAC' },
    { id: 'mp3', label: 'MP3' },
    { id: 'aac', label: 'AAC' },
    { id: 'opus', label: 'Opus' },
    { id: 'alac', label: 'ALAC' },
  ];

  function formatTime(seconds: number | null): string {
    if (seconds === null) return '--';
    return seconds.toFixed(1) + 's';
  }

  function handleExport(format: string) {
    showExportMenu = false;
    onExport(format);
  }

  function handleClickOutside(event: MouseEvent) {
    const target = event.target as HTMLElement;
    if (showExportMenu && !target.closest('.export-dropdown')) {
      showExportMenu = false;
    }
  }
</script>

<svelte:document onclick={handleClickOutside} />

<div class="audio-toolbar">
  <!-- Selection playback controls -->
  <div class="toolbar-group selection-controls">
    {#if isPlayingSelection}
      <button
        class="toolbar-btn"
        onclick={onStopSelection}
        aria-label={t('components.audioPlayer.clipExtraction.pauseSelection')}
      >
        <Pause size={14} />
      </button>
    {:else}
      <button
        class="toolbar-btn"
        disabled={!hasSelection}
        onclick={onPlaySelection}
        aria-label={t('components.audioPlayer.processing.playSelection')}
      >
        <Play size={14} />
      </button>
    {/if}
    <button
      class="toolbar-btn"
      disabled={!hasSelection}
      onclick={onSkipToSelection}
      aria-label={t('components.audioPlayer.processing.skipToStart')}
    >
      <SkipBack size={14} />
    </button>
    <span class="time-range" class:dimmed={!hasSelection}>
      {formatTime(selectionStart)} – {formatTime(selectionEnd)}
    </span>
    {#if hasSelection}
      <button
        class="toolbar-btn"
        onclick={onClearSelection}
        aria-label={t('components.audioPlayer.processing.clearSelection')}
      >
        <X size={14} />
      </button>
    {/if}
  </div>

  <!-- Processing controls -->
  <div class="toolbar-group processing-controls">
    <label class="toolbar-control">
      <Volume2 size={14} />
      <input
        type="range"
        min="-12"
        max="24"
        step="0.5"
        value={gainDb}
        oninput={e => onGainChange(Number(e.currentTarget.value))}
        class="gain-slider"
        aria-label={t('components.audioPlayer.processing.gain')}
      />
      <span class="control-value">{gainDb > 0 ? '+' : ''}{gainDb}dB</span>
    </label>

    <label class="toolbar-control">
      <AudioWaveform size={14} />
      <select
        value={denoise}
        onchange={e => onDenoiseChange(e.currentTarget.value)}
        class="denoise-select"
        disabled={isProcessing}
        aria-label={t('components.audioPlayer.processing.denoise')}
      >
        <option value="">{t('components.audioPlayer.processing.denoiseOff')}</option>
        <option value="light">{t('components.audioPlayer.processing.denoiseLight')}</option>
        <option value="medium">{t('components.audioPlayer.processing.denoiseMedium')}</option>
        <option value="heavy">{t('components.audioPlayer.processing.denoiseHeavy')}</option>
      </select>
      {#if isProcessing}
        <Loader2 size={14} class="animate-spin" />
      {/if}
    </label>

    <button
      class="toolbar-btn"
      class:active={normalize}
      onclick={onNormalizeToggle}
      disabled={isProcessing}
      aria-label={t('components.audioPlayer.processing.normalize')}
      title={t('components.audioPlayer.processing.normalizeTooltip')}
    >
      {#if isProcessing && normalize}
        <Loader2 size={14} class="animate-spin" />
      {:else}
        N
      {/if}
    </button>
  </div>

  <!-- Export controls -->
  <div class="toolbar-group export-controls">
    <div class="export-dropdown">
      <button
        class="toolbar-btn export-btn"
        class:error={extractionError !== null}
        disabled={!hasSelection || isExtracting}
        onclick={() => (showExportMenu = !showExportMenu)}
        aria-label={extractionError ?? t('components.audioPlayer.processing.export')}
        title={extractionError ?? ''}
        aria-expanded={showExportMenu}
        aria-haspopup="true"
      >
        {#if isExtracting}
          <Loader2 size={14} class="animate-spin" />
        {:else}
          <Download size={14} />
        {/if}
        <span>{t('components.audioPlayer.processing.export')}</span>
        <ChevronDown size={12} />
      </button>
      {#if showExportMenu && hasSelection}
        <div class="export-menu">
          {#each exportFormats as fmt (fmt.id)}
            <button class="export-option" onclick={() => handleExport(fmt.id)}>
              {fmt.label}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .audio-toolbar {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
    background: var(--color-base-200);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    flex-wrap: wrap;
    font-size: 0.8125rem;
  }

  .toolbar-group {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }

  .selection-controls {
    flex: 0 0 auto;
  }

  .processing-controls {
    flex: 1 1 auto;
    justify-content: center;
  }

  .export-controls {
    flex: 0 0 auto;
    margin-left: auto;
  }

  .toolbar-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    padding: 0.375rem 0.5rem;
    background: none;
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-selector);
    color: var(--color-base-content);
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 500;
    transition: all 0.15s ease;
  }

  .toolbar-btn:hover:not(:disabled) {
    background: var(--color-base-300);
  }

  .toolbar-btn:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }

  .toolbar-btn.active {
    background: var(--color-primary);
    color: var(--color-primary-content, #fff);
    border-color: var(--color-primary);
  }

  .toolbar-btn.error {
    border-color: var(--color-error, #ef4444);
    color: var(--color-error, #ef4444);
  }

  .toolbar-control {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    color: var(--color-base-content);
    opacity: 0.7;
  }

  .gain-slider {
    width: 80px;
    accent-color: var(--color-primary);
  }

  .control-value {
    font-size: 0.6875rem;
    font-variant-numeric: tabular-nums;
    min-width: 3rem;
  }

  .denoise-select {
    background: var(--color-base-100);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-selector);
    color: var(--color-base-content);
    padding: 0.25rem 0.375rem;
    font-size: 0.75rem;
  }

  .time-range {
    font-variant-numeric: tabular-nums;
    min-width: 7rem;
    text-align: center;
  }

  .time-range.dimmed {
    opacity: 0.35;
  }

  .export-dropdown {
    position: relative;
  }

  .export-menu {
    position: absolute;
    bottom: 100%;
    right: 0;
    margin-bottom: 0.25rem;
    background: var(--color-base-100);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
    z-index: 10;
    min-width: 100px;
  }

  .export-option {
    display: block;
    width: 100%;
    padding: 0.5rem 0.75rem;
    text-align: left;
    background: none;
    border: none;
    color: var(--color-base-content);
    cursor: pointer;
    font-size: 0.8125rem;
  }

  .export-option:hover {
    background: var(--color-base-200);
  }
</style>

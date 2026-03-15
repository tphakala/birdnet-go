<!--
  AudioToolbar.svelte - Persistent processing toolbar below spectrogram

  Provides: unified playback controls, selection range, gain/denoise/normalize processing,
  and export functionality. Inspired by iZotope RX toolbar layout.
-->
<script lang="ts">
  import {
    Play,
    Pause,
    SkipBack,
    Repeat,
    X,
    Download,
    Volume2,
    AudioWaveform,
    Loader2,
    ChevronDown,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    // Playback state
    isPlaying: boolean;
    isLoading: boolean;
    currentTime: number;
    duration: number;
    progress: number;
    loop: boolean;
    onPlayPause: () => void;
    onSeek: (_event: MouseEvent) => void;
    onLoopToggle: () => void;

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
    isPlaying,
    isLoading,
    currentTime,
    duration,
    progress,
    loop,
    onPlayPause,
    onSeek,
    onLoopToggle,
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

  let progressBarEl: HTMLDivElement;
  let showExportMenu = $state(false);
  let showDenoiseMenu = $state(false);

  const denoiseOptions = [
    { id: '', labelKey: 'components.audioPlayer.processing.denoiseOff' },
    { id: 'light', labelKey: 'components.audioPlayer.processing.denoiseLight' },
    { id: 'medium', labelKey: 'components.audioPlayer.processing.denoiseMedium' },
    { id: 'heavy', labelKey: 'components.audioPlayer.processing.denoiseHeavy' },
  ];

  let denoiseLabel = $derived(
    t(
      denoiseOptions.find(o => o.id === denoise)?.labelKey ??
        'components.audioPlayer.processing.denoiseOff'
    )
  );

  // Close export menu when selection is cleared
  $effect(() => {
    if (!hasSelection) {
      showExportMenu = false;
    }
  });

  const exportFormats = [
    { id: 'wav', label: 'WAV' },
    { id: 'flac', label: 'FLAC' },
    { id: 'mp3', label: 'MP3' },
    { id: 'aac', label: 'AAC' },
    { id: 'opus', label: 'Opus' },
    { id: 'alac', label: 'ALAC' },
  ];

  function formatSelectionTime(seconds: number | null): string {
    if (seconds === null) return '--';
    return seconds.toFixed(1) + 's';
  }

  function formatPlaybackTime(seconds: number): string {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  }

  function handleExport(format: string) {
    showExportMenu = false;
    onExport(format);
  }

  function handleClickOutside(event: MouseEvent) {
    const target = event.target as Node | null;
    if (!target || !('closest' in target)) return;
    const el = target as HTMLElement;
    if (showExportMenu && !el.closest('.export-dropdown')) {
      showExportMenu = false;
    }
    if (showDenoiseMenu && !el.closest('.denoise-dropdown')) {
      showDenoiseMenu = false;
    }
  }

  // Unified play handler: plays selection when selected, full audio otherwise
  function handlePlay() {
    if (hasSelection) {
      if (isPlayingSelection) {
        onStopSelection();
      } else {
        onPlaySelection();
      }
    } else {
      onPlayPause();
    }
  }

  // Unified playing state
  let isCurrentlyPlaying = $derived(hasSelection ? isPlayingSelection : isPlaying);
</script>

<svelte:document onclick={handleClickOutside} />

<div class="audio-toolbar">
  <!-- Playback controls: play, rewind, loop, time, progress -->
  <div class="toolbar-group playback-controls">
    <button
      class="toolbar-btn play-btn"
      onclick={handlePlay}
      disabled={isLoading}
      aria-label={isCurrentlyPlaying ? t('media.audio.pause') : t('media.audio.play')}
    >
      {#if isLoading}
        <Loader2 size={14} class="animate-spin" />
      {:else if isCurrentlyPlaying}
        <Pause size={14} />
      {:else}
        <Play size={14} />
      {/if}
    </button>
    <button
      class="toolbar-btn"
      disabled={!hasSelection}
      onclick={onSkipToSelection}
      aria-label={t('components.audioPlayer.processing.skipToStart')}
    >
      <SkipBack size={14} />
    </button>
    <button
      class="toolbar-btn"
      class:active={loop}
      onclick={onLoopToggle}
      aria-label={t('media.audio.loop')}
    >
      <Repeat size={14} />
    </button>
    <span class="playback-time"
      >{formatPlaybackTime(currentTime)} / {formatPlaybackTime(duration)}</span
    >
    <div
      bind:this={progressBarEl}
      class="progress-bar"
      role="slider"
      tabindex="0"
      aria-label={t('media.audio.seekProgress', {
        current: Math.floor(currentTime),
        total: Math.floor(duration),
      })}
      aria-valuemin={0}
      aria-valuemax={Math.floor(duration)}
      aria-valuenow={Math.floor(currentTime)}
      onclick={onSeek}
      onkeydown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          const rect = progressBarEl.getBoundingClientRect();
          const centerX = rect.left + rect.width / 2;
          onSeek({ clientX: centerX } as MouseEvent);
        }
      }}
    >
      <div class="progress-fill" style:width="{progress}%"></div>
    </div>
  </div>

  <!-- Selection range display -->
  <div class="toolbar-group selection-controls">
    <span class="time-range" class:dimmed={!hasSelection}>
      {formatSelectionTime(selectionStart)} – {formatSelectionTime(selectionEnd)}
    </span>
    <button
      class="toolbar-btn"
      class:invisible={!hasSelection}
      onclick={onClearSelection}
      disabled={!hasSelection}
      aria-label={t('components.audioPlayer.processing.clearSelection')}
    >
      <X size={14} />
    </button>
  </div>

  <!-- Processing controls -->
  <div class="toolbar-group processing-controls">
    <label class="toolbar-control" title={t('components.audioPlayer.processing.gain')}>
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

    <div class="denoise-dropdown" title={t('components.audioPlayer.processing.denoise')}>
      <button
        class="toolbar-btn"
        class:active={denoise !== ''}
        disabled={isProcessing}
        onclick={() => (showDenoiseMenu = !showDenoiseMenu)}
        aria-label={t('components.audioPlayer.processing.denoise')}
        aria-expanded={showDenoiseMenu}
        aria-haspopup="true"
      >
        <AudioWaveform size={14} />
        <span>{denoiseLabel}</span>
        <ChevronDown size={10} />
      </button>
      {#if showDenoiseMenu}
        <div class="denoise-menu">
          {#each denoiseOptions as opt (opt.id)}
            <button
              class="denoise-option"
              class:selected={denoise === opt.id}
              onclick={() => {
                onDenoiseChange(opt.id);
                showDenoiseMenu = false;
              }}
            >
              {t(opt.labelKey)}
            </button>
          {/each}
        </div>
      {/if}
    </div>

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
        disabled={isExtracting}
        onclick={() => (showExportMenu = !showExportMenu)}
        aria-label={t('components.audioPlayer.processing.export')}
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
      {#if extractionError}
        <span class="export-error" role="alert" aria-live="assertive">{extractionError}</span>
      {/if}
      {#if showExportMenu}
        <div class="export-menu">
          <button class="export-option" onclick={() => handleExport('original')}> Original </button>
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
    gap: 0.5rem;
    padding: 0.375rem 0.5rem;
    background: var(--color-base-200);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    font-size: 0.75rem;
  }

  .toolbar-group {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    flex-shrink: 0;
    white-space: nowrap;
  }

  .playback-controls {
    flex: 0 0 auto;
    gap: 0.375rem;
  }

  .play-btn {
    padding: 0.25rem;
  }

  .playback-time {
    font-size: 0.625rem;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    opacity: 0.7;
    width: 5rem;
  }

  .progress-bar {
    width: 80px;
    height: 5px;
    background: var(--color-base-300);
    border-radius: 3px;
    cursor: pointer;
    overflow: hidden;
    flex-shrink: 1;
    min-width: 40px;
  }

  .progress-fill {
    height: 100%;
    background: var(--color-primary);
    border-radius: 3px;
    transition: width 0.1s linear;
  }

  .selection-controls {
    flex: 0 0 auto;
  }

  .invisible {
    visibility: hidden;
    width: 0;
    padding: 0;
    border: 0;
    overflow: hidden;
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
    padding: 0.25rem 0.375rem;
    background: none;
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-selector);
    color: var(--color-base-content);
    cursor: pointer;
    font-size: 0.6875rem;
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

  .denoise-dropdown {
    position: relative;
  }

  .denoise-menu {
    position: absolute;
    bottom: 100%;
    left: 0;
    margin-bottom: 0.25rem;
    background: var(--color-base-100);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
    z-index: 10;
    min-width: 80px;
  }

  .denoise-option {
    display: block;
    width: 100%;
    padding: 0.375rem 0.625rem;
    text-align: left;
    background: none;
    border: none;
    color: var(--color-base-content);
    cursor: pointer;
    font-size: 0.75rem;
  }

  .denoise-option:hover {
    background: var(--color-base-200);
  }

  .denoise-option.selected {
    color: var(--color-primary);
    font-weight: 600;
  }

  .time-range {
    font-size: 0.625rem;
    font-variant-numeric: tabular-nums;
    width: 6rem;
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

  .export-error {
    font-size: 0.6875rem;
    color: var(--color-error, #ef4444);
    max-width: 12rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>

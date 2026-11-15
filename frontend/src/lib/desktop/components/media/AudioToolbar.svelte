<!--
  AudioToolbar.svelte - Persistent processing toolbar below spectrogram

  Provides: unified playback controls, selection range, gain/denoise/normalize processing,
  and export functionality. Inspired by iZotope RX toolbar layout.

  The bar wraps on its own content: a transport row (play, skip, loop, time, seek) and a
  processing row (selection, gain, denoise, normalize, export) share one line when there
  is room and stack otherwise, and each row wraps again inside a narrow host.
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
  import type { Action } from 'svelte/action';
  import { dropdown } from '$lib/utils/transitions';
  import { computeAnchorPosition, type HorizontalAlign } from '$lib/utils/anchorPosition';
  import { t } from '$lib/i18n';
  import { playButtonLabelKey } from '$lib/utils/audio';
  import { DENOISE_OPTIONS } from '$lib/utils/denoisePresets';
  import { formatTime } from '$lib/utils/formatters';
  import {
    clampSeconds,
    seekKeyAction,
    seekSliderValueText,
    seekTimeFromPointer,
    usableDuration,
  } from '$lib/utils/seek';

  interface Props {
    // Playback state
    isPlaying: boolean;
    isLoading: boolean;
    currentTime: number;
    duration: number;
    progress: number;
    loop: boolean;
    onPlayPause: () => void;
    // Seek to an absolute position, in seconds
    onSeek: (_seconds: number) => void;
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

  let denoiseLabel = $derived(
    t(
      DENOISE_OPTIONS.find(o => o.id === denoise)?.labelKey ??
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
  ];

  function formatSelectionTime(seconds: number | null): string {
    if (seconds === null) return '--';
    return seconds.toFixed(1) + 's';
  }

  // Usable clip length for the seek slider; 0 while the duration is unknown
  let seekMax = $derived(usableDuration(duration));
  let seekNow = $derived(clampSeconds(currentTime, seekMax));
  let seekValueText = $derived(seekSliderValueText(seekNow, seekMax));

  function handleSeekKeydown(e: KeyboardEvent) {
    const action = seekKeyAction(e, seekNow, seekMax);
    if (action.preventDefault) e.preventDefault();
    if (action.target !== null) onSeek(action.target);
  }

  function handleSeekClick(e: MouseEvent) {
    const target = seekTimeFromPointer(e.clientX, progressBarEl.getBoundingClientRect(), seekMax);
    if (target !== null) onSeek(target);
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
    // If full audio is already playing, always allow pausing it
    if (isPlaying && !isPlayingSelection) {
      onPlayPause();
      return;
    }
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
  let isCurrentlyPlaying = $derived(isPlayingSelection || isPlaying);

  // Gap between a menu and its dropdown wrapper; the menus' margin-block in the CSS below
  const MENU_GAP_PX = 4;
  // Distance a menu keeps from the viewport edges
  const MENU_VIEWPORT_MARGIN_PX = 8;

  /**
   * Keeps a menu inside the viewport. The menu stays absolutely positioned inside
   * its `position: relative` dropdown wrapper, after its trigger, so Tab order and
   * the outside click check are unchanged. Its left edge is aligned to the trigger
   * and clamped into the viewport; it opens above the wrapper unless only the room
   * below fits it, and its height is capped to the room on the chosen side. The
   * position is recomputed when the window is resized or anything scrolls.
   */
  const anchorMenu: Action<HTMLElement, HorizontalAlign> = (menu, align = 'start') => {
    function place(event?: Event): void {
      // Scrolling inside the menu does not move it
      if (event?.target instanceof Node && menu.contains(event.target)) return;
      const wrapper = menu.parentElement;
      const trigger = wrapper?.querySelector(':scope > button');
      if (!wrapper || !(trigger instanceof HTMLElement)) return;

      // Measure the natural height, not the one capped by the previous pass; clearing
      // the cap resets the menu's own scroll position, so keep it
      const scrollTop = menu.scrollTop;
      menu.style.maxHeight = '';
      const height = menu.offsetHeight;
      const wrapperRect = wrapper.getBoundingClientRect();
      const { left } = computeAnchorPosition({
        triggerRect: trigger.getBoundingClientRect(),
        floatingHeight: height,
        floatingWidth: menu.offsetWidth,
        align,
        viewportMargin: MENU_VIEWPORT_MARGIN_PX,
      });
      menu.style.left = `${left - wrapperRect.left}px`;
      menu.style.right = 'auto';

      // Unlike computeAnchorPosition, which prefers below, the menu opens above as it
      // always has. The room is measured from the wrapper: the menu hangs off its
      // edges, and an export error message can sit between the trigger and the bottom.
      const roomAbove = wrapperRect.top - MENU_VIEWPORT_MARGIN_PX;
      const roomBelow = window.innerHeight - wrapperRect.bottom - MENU_VIEWPORT_MARGIN_PX;
      const below = roomAbove < height + MENU_GAP_PX && roomBelow > roomAbove;
      menu.style.top = below ? '100%' : 'auto';
      menu.style.bottom = below ? 'auto' : '100%';
      menu.style.maxHeight = `${Math.max(0, (below ? roomBelow : roomAbove) - MENU_GAP_PX)}px`;
      menu.scrollTop = scrollTop;
    }

    place();
    window.addEventListener('resize', place);
    // Captured, so a scroll of any container that moves the trigger is seen too
    window.addEventListener('scroll', place, true);
    return {
      destroy() {
        window.removeEventListener('resize', place);
        window.removeEventListener('scroll', place, true);
      },
    };
  };
</script>

<svelte:document onclick={handleClickOutside} />

<div class="audio-toolbar">
  <!-- Transport row: play, rewind, loop, time, seek -->
  <div class="toolbar-row playback-controls">
    <div class="toolbar-group">
      <button
        class="toolbar-btn play-btn"
        onclick={handlePlay}
        disabled={isLoading}
        aria-label={t(playButtonLabelKey(isLoading, isCurrentlyPlaying))}
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
        type="button"
        class="toolbar-btn"
        class:active={loop}
        aria-pressed={loop}
        onclick={onLoopToggle}
        aria-label={t('media.audio.loop')}
      >
        <Repeat size={14} />
      </button>
    </div>
    <span class="playback-time">{formatTime(currentTime)} / {formatTime(seekMax)}</span>
    <div
      bind:this={progressBarEl}
      class="progress-bar"
      role="slider"
      tabindex="0"
      aria-label={t('media.audio.seek')}
      aria-valuemin={0}
      aria-valuemax={seekMax}
      aria-valuenow={seekNow}
      aria-disabled={seekMax <= 0 ? 'true' : undefined}
      aria-valuetext={seekValueText}
      onclick={handleSeekClick}
      onkeydown={handleSeekKeydown}
    >
      <div class="progress-track">
        <div class="progress-fill" style:width="{progress}%"></div>
      </div>
    </div>
  </div>

  <!-- Processing row: selection range, gain, denoise, normalize, export -->
  <div class="toolbar-row processing-controls">
    <div class="toolbar-group selection-controls">
      <span class="time-range" class:dimmed={!hasSelection}>
        {formatSelectionTime(selectionStart)} - {formatSelectionTime(selectionEnd)}
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
        aria-expanded={showDenoiseMenu}
        aria-haspopup="true"
      >
        <AudioWaveform size={14} />
        <!-- The name is "Denoise Light": the control, then the visible state it shows -->
        <span class="sr-only">{t('components.audioPlayer.processing.denoise')}</span>
        <!-- Hidden sizers reserve the longest label's width, so the button and the
             rows around it keep their size when the preset changes. Their text sits in
             an attribute, not the DOM, so the button's name and text stay the same. -->
        <span class="denoise-label">
          <span class="denoise-label-current">{denoiseLabel}</span>
          {#each DENOISE_OPTIONS as opt (opt.id)}
            <span class="denoise-label-sizer" aria-hidden="true" data-label={t(opt.labelKey)}
            ></span>
          {/each}
        </span>
        <ChevronDown size={10} />
      </button>
      {#if showDenoiseMenu}
        <div
          class="denoise-menu"
          use:anchorMenu={'start'}
          in:dropdown={{ y: -4, duration: 120 }}
          out:dropdown={{ y: -4, duration: 80 }}
        >
          {#each DENOISE_OPTIONS as opt (opt.id)}
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
      type="button"
      class="toolbar-btn"
      class:active={normalize}
      aria-pressed={normalize}
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
        <span class="export-error" role="alert">{extractionError}</span>
      {/if}
      {#if showExportMenu}
        <div
          class="export-menu"
          use:anchorMenu={'end'}
          in:dropdown={{ y: -4, duration: 120 }}
          out:dropdown={{ y: -4, duration: 80 }}
        >
          <button class="export-option" onclick={() => handleExport('original')}>
            {t('components.audioPlayer.processing.exportOriginal')}
          </button>
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
    /* Hit-area floor for every control, in px rather than rem: WCAG target size is in
       CSS px, and a rem floor would double each icon button at 200 percent text. */
    --audio-toolbar-target: 44px;

    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 0.75rem;
    padding: 0.5rem;
    background: var(--color-base-200);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    font-size: 0.75rem;
  }

  .toolbar-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.5rem;
    min-width: 0;
  }

  /* Grows to fill its line; 24rem is the width it asks for before the processing
     row may sit beside it. */
  .playback-controls {
    flex: 1 1 24rem;
  }

  .processing-controls {
    flex: 0 1 auto;
  }

  .toolbar-group {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 0.25rem;
    white-space: nowrap;
  }

  .playback-time {
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    opacity: 0.7;
    min-width: 5rem;
  }

  /* The slider box is the 44 px hit area; the visible track is a full-width child,
     so a click maps onto the track exactly (no padding or border here). */
  .progress-bar {
    display: flex;
    flex: 1 1 6rem;
    align-items: center;
    min-width: 6rem;
    height: var(--audio-toolbar-target);
    cursor: pointer;
  }

  .progress-track {
    flex: 1 1 auto;
    height: 5px;
    background: var(--color-base-300);
    border-radius: 3px;
    overflow: hidden;
  }

  .progress-fill {
    height: 100%;
    background: var(--color-primary);
    border-radius: 3px;
    transition: width 0.1s linear;
  }

  /* Hidden, but keeps its box, so the bar does not move when a selection appears */
  .invisible {
    visibility: hidden;
  }

  .toolbar-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    min-width: var(--audio-toolbar-target);
    min-height: var(--audio-toolbar-target);
    padding: 0.25rem 0.5rem;
    background: none;
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-selector);
    color: var(--color-base-content);
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
    transition: all 0.15s ease;
  }

  .toolbar-btn:not(.active):hover:not(:disabled) {
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

  .toolbar-btn.active:hover:not(:disabled) {
    background: var(--color-primary-hover);
    border-color: var(--color-primary-hover);
  }

  .toolbar-btn.error {
    border-color: var(--color-error, #ef4444);
    color: var(--text-error);
  }

  .toolbar-control {
    display: flex;
    flex: 0 1 auto;
    align-items: center;
    gap: 0.25rem;
    min-width: 0;
    color: var(--color-base-content);
    opacity: 0.7;
    white-space: nowrap;
  }

  /* Shrinkable, so the gain group still fits a very narrow bar */
  .gain-slider {
    flex: 1 1 5rem;
    min-width: 3rem;
    height: var(--audio-toolbar-target);
    margin: 0;
    accent-color: var(--color-primary);
  }

  .control-value {
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    min-width: 3rem;
  }

  .denoise-dropdown,
  .export-dropdown {
    position: relative;
    flex: 0 1 auto;
    min-width: 0;
  }

  /* Stacks the current label over hidden copies of every label, like a native select */
  .denoise-label {
    display: inline-grid;
    justify-items: start;
  }

  .denoise-label-current,
  .denoise-label-sizer {
    grid-area: 1 / 1;
  }

  .denoise-label-sizer {
    visibility: hidden;
  }

  .denoise-label-sizer::after {
    content: attr(data-label);
  }

  /* anchorMenu places the menus inline: left, the side and max-height */
  .denoise-menu,
  .export-menu {
    position: absolute;
    width: max-content;
    max-width: calc(100vw - 1rem);
    overflow-y: auto;
    margin-block: 0.25rem;
    background: var(--color-base-100);
    border: 1px solid var(--color-base-300);
    border-radius: var(--radius-field);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
    white-space: nowrap;
    z-index: 10;
  }

  .denoise-menu {
    min-width: 80px;
  }

  .export-menu {
    min-width: 100px;
  }

  .denoise-option,
  .export-option {
    display: block;
    width: 100%;
    min-height: var(--audio-toolbar-target);
    text-align: left;
    background: none;
    border: none;
    color: var(--color-base-content);
    cursor: pointer;
  }

  .denoise-option {
    padding: 0.375rem 0.625rem;
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
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    min-width: 6.5rem;
    text-align: center;
  }

  .time-range.dimmed {
    opacity: 0.35;
  }

  .export-option {
    padding: 0.5rem 0.75rem;
    font-size: 0.8125rem;
  }

  .export-option:hover {
    background: var(--color-base-200);
  }

  .export-error {
    display: block;
    font-size: 0.75rem;
    color: var(--text-error);
    max-width: 12rem;
    overflow-wrap: anywhere;
  }
</style>

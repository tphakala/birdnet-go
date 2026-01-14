<!--
  AudioSettingsButton.svelte

  A button that opens audio settings popup for gain, high-pass filter, and playback speed control.
  Designed to be placed outside overflow-hidden containers with fixed positioning for the menu.

  Props:
  - gainValue: number - Current gain value in dB
  - filterFreq: number - Current high-pass filter frequency in Hz
  - playbackSpeed: number - Current playback speed multiplier
  - onGainChange: (value: number) => void - Callback when gain changes
  - onFilterChange: (value: number) => void - Callback when filter changes
  - onSpeedChange: (value: number) => void - Callback when speed changes
  - disabled?: boolean - Whether the button is disabled (e.g., AudioContext not available)
-->
<script lang="ts">
  import { Volume2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    gainValue: number;
    filterFreq: number;
    playbackSpeed: number;
    onGainChange: (_value: number) => void;
    onFilterChange: (_value: number) => void;
    onSpeedChange: (_value: number) => void;
    disabled?: boolean;
    onMenuOpen?: () => void;
    onMenuClose?: () => void;
  }

  let {
    gainValue,
    filterFreq,
    playbackSpeed,
    onGainChange,
    onFilterChange,
    onSpeedChange,
    disabled = false,
    onMenuOpen,
    onMenuClose,
  }: Props = $props();

  // Constants
  const GAIN_MAX_DB = 24;
  const GAIN_MIN_DB = -20;
  const FILTER_HP_MIN_FREQ = 20;
  const FILTER_HP_MAX_FREQ = 5000;
  const SPEED_OPTIONS = [0.5, 0.75, 1.0, 1.25, 1.5] as const;
  const DEFAULT_SPEED = 1.0;

  // Generate unique ID for this component instance
  const instanceId = Math.random().toString(36).slice(2, 9);
  const gainSliderId = `gain-slider-${instanceId}`;
  const filterSliderId = `filter-slider-${instanceId}`;

  let showSettings = $state(false);
  let buttonElement: HTMLButtonElement;
  // svelte-ignore non_reactive_update
  let menuElement: HTMLDivElement;
  // svelte-ignore non_reactive_update
  let gainSliderElement: HTMLInputElement;

  // Check if settings have been modified from defaults
  const hasModifiedSettings = $derived(
    gainValue !== 0 || filterFreq > FILTER_HP_MIN_FREQ || playbackSpeed !== DEFAULT_SPEED
  );

  function updateMenuPosition() {
    if (!menuElement || !buttonElement) return;

    const buttonRect = buttonElement.getBoundingClientRect();
    const spaceBelow = window.innerHeight - buttonRect.bottom;
    const spaceAbove = buttonRect.top;
    const menuHeight = menuElement.offsetHeight;

    menuElement.style.position = 'fixed';
    menuElement.style.zIndex = '9999';

    // Position below or above based on available space
    if (spaceBelow < menuHeight && spaceAbove > spaceBelow) {
      menuElement.style.bottom = `${window.innerHeight - buttonRect.top + 8}px`;
      menuElement.style.top = 'auto';
    } else {
      menuElement.style.top = `${buttonRect.bottom + 8}px`;
      menuElement.style.bottom = 'auto';
    }

    // Align to right edge of button
    menuElement.style.left = 'auto';
    menuElement.style.right = `${window.innerWidth - buttonRect.right}px`;
  }

  function handleToggle(event: MouseEvent) {
    event.stopPropagation();
    showSettings = !showSettings;

    if (showSettings) {
      onMenuOpen?.();
      globalThis.requestAnimationFrame(() => {
        updateMenuPosition();
        // Focus first slider when dialog opens
        gainSliderElement?.focus();
      });
    } else {
      onMenuClose?.();
    }
  }

  function handleClickOutside(event: MouseEvent) {
    if (
      showSettings &&
      menuElement &&
      !menuElement.contains(event.target as Node) &&
      buttonElement &&
      !buttonElement.contains(event.target as Node)
    ) {
      showSettings = false;
      onMenuClose?.();
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (showSettings && event.key === 'Escape') {
      showSettings = false;
      onMenuClose?.();
      buttonElement?.focus();
    }
  }

  function handleReset() {
    onGainChange(0);
    onFilterChange(FILTER_HP_MIN_FREQ);
    onSpeedChange(DEFAULT_SPEED);
  }

  $effect(() => {
    if (showSettings) {
      function handleResize() {
        updateMenuPosition();
      }

      document.addEventListener('click', handleClickOutside);
      document.addEventListener('keydown', handleKeydown);
      window.addEventListener('resize', handleResize);
      window.addEventListener('scroll', handleResize, true);

      return () => {
        document.removeEventListener('click', handleClickOutside);
        document.removeEventListener('keydown', handleKeydown);
        window.removeEventListener('resize', handleResize);
        window.removeEventListener('scroll', handleResize, true);
      };
    }
  });

  // Cleanup on unmount
  $effect(() => {
    return () => {
      if (showSettings) {
        showSettings = false;
      }
    };
  });
</script>

<div>
  <button
    bind:this={buttonElement}
    onclick={handleToggle}
    class="audio-settings-trigger"
    class:active={showSettings}
    aria-label={t('media.audio.settings')}
    aria-haspopup="true"
    aria-expanded={showSettings}
    {disabled}
  >
    <Volume2 class="size-5" />
    {#if hasModifiedSettings}
      <span class="settings-indicator"></span>
    {/if}
  </button>

  {#if showSettings}
    <div
      bind:this={menuElement}
      class="settings-menu"
      onclick={e => e.stopPropagation()}
      onkeydown={e => {
        if (e.key === 'Escape') {
          showSettings = false;
        }
        e.stopPropagation();
      }}
      role="dialog"
      aria-label={t('media.audio.settings')}
      tabindex="-1"
    >
      <div class="settings-title">{t('media.audio.settings')}</div>

      <!-- Gain control -->
      <div class="setting-item">
        <label class="setting-label" for={gainSliderId}>
          {t('media.audio.volumeGain', { value: gainValue })}
        </label>
        <div class="slider-container">
          <input
            bind:this={gainSliderElement}
            id={gainSliderId}
            type="range"
            min={GAIN_MIN_DB}
            max={GAIN_MAX_DB}
            step="1"
            value={gainValue}
            oninput={e => onGainChange(Number((e.target as HTMLInputElement).value))}
            class="slider"
          />
          <span class="slider-value">{gainValue > 0 ? '+' : ''}{gainValue} dB</span>
        </div>
      </div>

      <!-- High-pass filter control -->
      <div class="setting-item">
        <label class="setting-label" for={filterSliderId}>
          {t('media.audio.highPassFilter', { freq: Math.round(filterFreq) })}
        </label>
        <div class="slider-container">
          <input
            id={filterSliderId}
            type="range"
            min={FILTER_HP_MIN_FREQ}
            max={FILTER_HP_MAX_FREQ}
            step="10"
            value={filterFreq}
            oninput={e => onFilterChange(Number((e.target as HTMLInputElement).value))}
            class="slider"
          />
          <span class="slider-value">{Math.round(filterFreq)} Hz</span>
        </div>
      </div>

      <!-- Playback speed control -->
      <div class="setting-item">
        <span class="setting-label">
          {t('media.audio.playbackSpeed')}: {playbackSpeed}×
        </span>
        <div
          class="speed-button-group"
          role="radiogroup"
          aria-label={t('media.audio.playbackSpeed')}
        >
          {#each SPEED_OPTIONS as speed}
            <button
              class="speed-option"
              class:active={playbackSpeed === speed}
              onclick={() => onSpeedChange(speed)}
              role="radio"
              aria-checked={playbackSpeed === speed}
            >
              {speed}×
            </button>
          {/each}
        </div>
      </div>

      <!-- Reset button -->
      <button class="reset-button" onclick={handleReset} disabled={!hasModifiedSettings}>
        {t('common.reset')}
      </button>
    </div>
  {/if}
</div>

<style>
  .audio-settings-trigger {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: 9999px;
    background-color: rgb(0 0 0 / 0.5);
    backdrop-filter: blur(4px);
    color: white;
    transition: background-color 0.15s ease;
  }

  .audio-settings-trigger:hover {
    background-color: rgb(51 65 85 / 0.8);
  }

  .audio-settings-trigger.active {
    background-color: rgb(59 130 246 / 0.6);
  }

  .audio-settings-trigger:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* Settings indicator dot */
  .settings-indicator {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: rgb(59 130 246);
    box-shadow: 0 0 4px rgb(59 130 246 / 0.6);
    border: 1px solid white;
  }

  /* Settings popup menu */
  .settings-menu {
    position: fixed;
    min-width: 14rem;
    padding: 0.75rem;
    background-color: rgb(30 41 59 / 0.95);
    border: 1px solid rgb(51 65 85);
    border-radius: 0.5rem;
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.4);
    backdrop-filter: blur(8px);
    animation: menuFadeIn 0.15s ease-out;
    z-index: 9999 !important;
  }

  :global([data-theme='light']) .settings-menu {
    background-color: rgb(255 255 255 / 0.95);
    border-color: var(--color-base-300);
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.15);
  }

  @keyframes menuFadeIn {
    from {
      opacity: 0;
      transform: scale(0.95);
    }

    to {
      opacity: 1;
      transform: scale(1);
    }
  }

  .settings-title {
    font-size: 0.75rem;
    font-weight: 600;
    color: rgb(148 163 184);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 0.5rem;
    padding-bottom: 0.375rem;
    border-bottom: 1px solid rgb(51 65 85);
  }

  :global([data-theme='light']) .settings-title {
    color: var(--color-base-content);
    border-color: var(--color-base-300);
  }

  .setting-item {
    margin-bottom: 0.625rem;
  }

  .setting-label {
    display: block;
    font-size: 0.75rem;
    color: rgb(226 232 240);
    margin-bottom: 0.25rem;
  }

  :global([data-theme='light']) .setting-label {
    color: var(--color-base-content);
  }

  .slider-container {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .slider {
    flex: 1;
    height: 4px;
    border-radius: 2px;
    background: rgb(51 65 85);
    appearance: none;
    cursor: pointer;
  }

  :global([data-theme='light']) .slider {
    background: var(--color-base-300);
  }

  .slider::-webkit-slider-thumb {
    appearance: none;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: rgb(59 130 246);
    cursor: pointer;
    transition: transform 0.1s ease;
  }

  .slider::-webkit-slider-thumb:hover {
    transform: scale(1.15);
  }

  .slider::-moz-range-thumb {
    width: 14px;
    height: 14px;
    border: none;
    border-radius: 50%;
    background: rgb(59 130 246);
    cursor: pointer;
  }

  .slider-value {
    font-size: 0.7rem;
    font-weight: 500;
    color: rgb(148 163 184);
    min-width: 3.5rem;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  :global([data-theme='light']) .slider-value {
    color: var(--color-base-content);
  }

  .reset-button {
    width: 100%;
    padding: 0.375rem 0.5rem;
    margin-top: 0.375rem;
    font-size: 0.75rem;
    color: rgb(148 163 184);
    background-color: rgb(51 65 85 / 0.5);
    border-radius: 0.25rem;
    transition: all 0.15s ease;
  }

  .reset-button:hover:not(:disabled) {
    background-color: rgb(51 65 85);
    color: white;
  }

  .reset-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  :global([data-theme='light']) .reset-button {
    color: var(--color-base-content);
    background-color: var(--color-base-200);
  }

  :global([data-theme='light']) .reset-button:hover:not(:disabled) {
    background-color: var(--color-base-300);
  }

  /* Speed button group */
  .speed-button-group {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  .speed-option {
    padding: 0.25rem 0.5rem;
    font-size: 0.7rem;
    font-weight: 500;
    color: rgb(148 163 184);
    background-color: rgb(51 65 85 / 0.5);
    border-radius: 0.25rem;
    transition: all 0.15s ease;
    font-variant-numeric: tabular-nums;
  }

  .speed-option:hover {
    background-color: rgb(51 65 85);
    color: white;
  }

  .speed-option.active {
    background-color: rgb(59 130 246);
    color: white;
  }

  :global([data-theme='light']) .speed-option {
    color: var(--color-base-content);
    background-color: var(--color-base-200);
  }

  :global([data-theme='light']) .speed-option:hover {
    background-color: var(--color-base-300);
  }

  :global([data-theme='light']) .speed-option.active {
    background-color: rgb(59 130 246);
    color: white;
  }
</style>

<!--
  SpectrogramControls.svelte

  User controls for spectrogram configuration.
  Reusable across LiveStreamPage (full controls) and MiniSpectrogram (compact).
  Uses callback props for state changes.

  Props:
  - frequencyRange: Display frequency range [min, max] in Hz
  - colorMap: Color map name
  - gainDb: Gain in dB (hidden in compact mode)
  - audioOutput: Audio output enabled
  - compact: Compact mode (hides gain slider)
  - onFrequencyRangeChange: Callback when frequency range changes
  - onColorMapChange: Callback when color map changes
  - onGainChange: Callback when gain changes
  - onAudioOutputToggle: Callback when audio output is toggled
-->

<script lang="ts">
  /**
   * SpectrogramControls — User controls for spectrogram configuration
   *
   * Reusable across LiveStreamPage (full controls) and MiniSpectrogram (compact).
   * Uses callback props for state changes.
   */

  import { Volume2, VolumeX } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';

  interface Props {
    /** Display frequency range [min, max] in Hz */
    frequencyRange: [number, number];
    /** Color map name */
    colorMap: ColorMapName;
    /** Gain in dB (hidden in compact mode) */
    gainDb?: number;
    /** Audio output enabled */
    audioOutput?: boolean;
    /** Compact mode — hides gain slider */
    compact?: boolean;
    /** Callbacks */
    onFrequencyRangeChange?: (_range: [number, number]) => void;
    onColorMapChange?: (_map: ColorMapName) => void;
    onGainChange?: (_db: number) => void;
    onAudioOutputToggle?: () => void;
  }

  let {
    frequencyRange,
    colorMap,
    gainDb = 0,
    audioOutput = false,
    compact = false,
    onFrequencyRangeChange,
    onColorMapChange,
    onGainChange,
    onAudioOutputToggle,
  }: Props = $props();

  const MAX_FREQ = 24000;
  const MAX_GAIN_DB = 24;

  function handleMinFreqChange(e: Event) {
    const value = parseInt((e.target as HTMLInputElement).value);
    const newRange: [number, number] = [value, frequencyRange[1]];
    onFrequencyRangeChange?.(newRange);
  }

  function handleMaxFreqChange(e: Event) {
    const value = parseInt((e.target as HTMLInputElement).value);
    const newRange: [number, number] = [frequencyRange[0], value];
    onFrequencyRangeChange?.(newRange);
  }

  function handleColorMapChange(e: Event) {
    const value = (e.target as HTMLSelectElement).value as ColorMapName;
    onColorMapChange?.(value);
  }

  function handleGainChange(e: Event) {
    const value = parseFloat((e.target as HTMLInputElement).value);
    onGainChange?.(value);
  }
</script>

<div class="flex flex-wrap items-center gap-3 text-sm">
  <!-- Frequency range -->
  <div class="flex items-center gap-2">
    <label for="spectrogram-freq-min" class="text-base-content/70 whitespace-nowrap">
      {t('spectrogram.controls.frequencyRange')}
    </label>
    <input
      id="spectrogram-freq-min"
      type="range"
      min="0"
      max={frequencyRange[1] - 500}
      step="500"
      value={frequencyRange[0]}
      oninput={handleMinFreqChange}
      class="range range-xs w-20"
      aria-label={t('spectrogram.controls.frequencyRange') + ' min'}
    />
    <span class="text-base-content/50 tabular-nums">
      {(frequencyRange[0] / 1000).toFixed(1)}&ndash;{(frequencyRange[1] / 1000).toFixed(1)} kHz
    </span>
    <input
      id="spectrogram-freq-max"
      type="range"
      min={frequencyRange[0] + 500}
      max={MAX_FREQ}
      step="500"
      value={frequencyRange[1]}
      oninput={handleMaxFreqChange}
      class="range range-xs w-20"
      aria-label={t('spectrogram.controls.frequencyRange') + ' max'}
    />
  </div>

  <!-- Color map -->
  <div class="flex items-center gap-2">
    <label for="spectrogram-colormap" class="text-base-content/70 whitespace-nowrap">
      {t('spectrogram.controls.colorMap')}
    </label>
    <select
      id="spectrogram-colormap"
      value={colorMap}
      onchange={handleColorMapChange}
      class="select select-xs"
    >
      <option value="magma">{t('spectrogram.colorMaps.magma')}</option>
      <option value="inferno">{t('spectrogram.colorMaps.inferno')}</option>
      <option value="viridis">{t('spectrogram.colorMaps.viridis')}</option>
    </select>
  </div>

  <!-- Gain (hidden in compact mode) -->
  {#if !compact}
    <div class="flex items-center gap-2">
      <label for="spectrogram-gain" class="text-base-content/70 whitespace-nowrap">
        {t('spectrogram.controls.gain')}
      </label>
      <input
        id="spectrogram-gain"
        type="range"
        min="0"
        max={MAX_GAIN_DB}
        step="1"
        value={gainDb}
        oninput={handleGainChange}
        class="range range-xs w-20"
      />
      <span class="text-base-content/50 tabular-nums">{gainDb} dB</span>
    </div>
  {/if}

  <!-- Mute/Unmute toggle -->
  <button
    onclick={onAudioOutputToggle}
    class="btn btn-ghost btn-xs"
    aria-label={audioOutput ? t('spectrogram.controls.mute') : t('spectrogram.controls.unmute')}
  >
    {#if audioOutput}
      <Volume2 class="size-4" />
    {:else}
      <VolumeX class="size-4" />
    {/if}
  </button>
</div>

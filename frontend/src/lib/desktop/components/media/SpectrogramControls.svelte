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

  import { Tag, Volume2, VolumeX } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import type { ColorMapName } from '$lib/utils/spectrogramColorMaps';

  // CSS gradients approximating each colormap (sampled at key stops)
  const COLOR_MAP_GRADIENTS: Record<ColorMapName, string> = {
    inferno: 'linear-gradient(to right, #000004, #420a68, #932667, #dd513a, #fcffa4)',
    viridis: 'linear-gradient(to right, #440154, #31688e, #35b779, #90d743, #fde725)',
    grayscale: 'linear-gradient(to right, #ffffff, #808080, #000000)',
  };

  const colorMapOptions: SelectOption[] = [
    { value: 'inferno', label: 'Inferno' },
    { value: 'viridis', label: 'Viridis' },
    { value: 'grayscale', label: 'Grayscale' },
  ];

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
    /** Whether detection labels are shown */
    showDetectionLabels?: boolean;
    /** Toggle detection label visibility */
    onDetectionLabelsToggle?: () => void;
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
    showDetectionLabels = true,
    onDetectionLabelsToggle,
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

  function handleColorMapChange(value: string | string[]) {
    const selected = (Array.isArray(value) ? value[0] : value) as ColorMapName;
    onColorMapChange?.(selected);
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
      aria-label={t('spectrogram.controls.frequencyRangeMin')}
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
      aria-label={t('spectrogram.controls.frequencyRangeMax')}
    />
  </div>

  <!-- Color map -->
  <div class="flex items-center gap-2">
    <span class="text-base-content/70 whitespace-nowrap">
      {t('spectrogram.controls.colorMap')}
    </span>
    <SelectDropdown
      options={colorMapOptions}
      value={colorMap}
      variant="select"
      size="xs"
      groupBy={false}
      onChange={handleColorMapChange}
      className="w-36"
    >
      {#snippet renderOption(option)}
        <div class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-12 shrink-0 rounded-sm"
            style:background={COLOR_MAP_GRADIENTS[option.value as ColorMapName]}
          ></span>
          <span>{t(`spectrogram.colorMaps.${option.value}`)}</span>
        </div>
      {/snippet}
      {#snippet renderSelected(options)}
        {#if options.length > 0}
          <span class="flex items-center gap-2">
            <span
              class="inline-block h-3 w-12 shrink-0 rounded-sm"
              style:background={COLOR_MAP_GRADIENTS[options[0].value as ColorMapName]}
            ></span>
            <span>{t(`spectrogram.colorMaps.${options[0].value}`)}</span>
          </span>
        {/if}
      {/snippet}
    </SelectDropdown>
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
    type="button"
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

  <!-- Detection labels toggle -->
  {#if onDetectionLabelsToggle}
    <button
      type="button"
      onclick={onDetectionLabelsToggle}
      class="rounded p-1.5 transition-colors {showDetectionLabels
        ? 'bg-[var(--color-primary)]/20 text-[var(--color-primary)]'
        : 'text-[var(--color-base-content)]/60 hover:bg-[var(--color-base-200)]'}"
      aria-label={t('spectrogram.labels.toggle')}
      title={t('spectrogram.labels.toggle')}
    >
      <Tag class="size-4" />
    </button>
  {/if}
</div>

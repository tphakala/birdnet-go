<!--
  Stream Channel Controls

  Purpose: Render the per-stream channel handling UI (downmix/left/right selector,
  detected audio format, energy analysis and warnings) and adapt it to the
  detected channel count.

  Gating rules (see channelUIState in ./streamChannel.ts):
  - Unknown channel count (stream not tested): hide the selector, prompt to test.
  - Mono source: hide the selector (all modes are identical), show a short note.
  - Multi-channel source: show the selector plus the energy analysis and warnings.
  - A stream already set to left/right keeps the selector visible even when the
    source is offline or probes as mono, so the existing choice stays editable.

  Analysis state (isAnalyzing/analysisResult/analysisError) is owned by the parent
  form because it drives the API call and request-cancellation logic.

  @component
-->
<script lang="ts">
  import { Search, Loader2, AlertTriangle, CircleCheck } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import SelectDropdown from './SelectDropdown.svelte';
  import { getChannelModeOptions } from './streamOptions';
  import { channelUIState, sampleRateKhz } from './streamChannel';
  import type { ChannelMode, ChannelAnalysis } from '$lib/stores/settings';

  interface Props {
    /** Currently selected channel mode. */
    channelMode: ChannelMode;
    /** Detected channel count: 0 = unknown/untested, 1 = mono, >1 = multi-channel. */
    channels: number;
    /** Detected source sample rate in Hz, when known from a stream test. */
    sampleRate?: number;
    /** URL passed to the channel analyzer when "Detect best channel" is clicked. */
    analyzeUrl: string;
    isAnalyzing: boolean;
    analysisResult: ChannelAnalysis | null;
    analysisError: string | null;
    disabled?: boolean;
    onChange: (_mode: ChannelMode) => void;
    onAnalyze: (_url: string) => void;
  }

  let {
    channelMode,
    channels,
    sampleRate,
    analyzeUrl,
    isAnalyzing,
    analysisResult,
    analysisError,
    disabled = false,
    onChange,
    onAnalyze,
  }: Props = $props();

  let ui = $derived(channelUIState(channels, channelMode));

  // Localized "Mono (1 channel)" / "Stereo (2 channels)" / "N channels" label.
  let channelsText = $derived.by(() => {
    if (channels === 1) return t('settings.audio.streams.format.mono');
    if (channels === 2) return t('settings.audio.streams.format.stereo');
    return t('settings.audio.streams.format.multi', { count: channels });
  });

  // Render kHz without trailing zeros: 48000 -> "48", 44100 -> "44.1".
  let formatText = $derived.by(() => {
    if (sampleRate && sampleRate > 0) {
      return t('settings.audio.streams.format.withSampleRate', {
        rate: sampleRateKhz(sampleRate),
        channels: channelsText,
      });
    }
    return channelsText;
  });

  // Also honor the parent form's disabled state so the detect button cannot be
  // triggered while the whole form is disabled.
  let canAnalyze = $derived(!disabled && !isAnalyzing && (analyzeUrl ?? '').trim().length > 0);

  // Energy bar geometry: dBFS spans -96 (silence) to 0 (full scale); clamp the
  // rendered width between a minimum (so a near-silent channel stays visible)
  // and the full track width.
  const DBFS_RANGE = 96;
  const MIN_BAR_PERCENT = 2;
  const MAX_BAR_PERCENT = 100;
</script>

<div class="space-y-2">
  <!-- Detected audio format -->
  <p class="text-xs text-[var(--color-base-content)]/70">
    <span class="font-medium text-[var(--color-base-content)]"
      >{t('settings.audio.streams.format.label')}:</span
    >
    {channels < 1 ? t('settings.audio.streams.format.untested') : formatText}
  </p>

  {#if ui.showSelector}
    <SelectDropdown
      value={channelMode}
      label={t('settings.audio.streams.channelMode.label')}
      options={getChannelModeOptions()}
      {disabled}
      onChange={value => onChange(value as ChannelMode)}
      groupBy={false}
      menuSize="sm"
      helpText={t('settings.audio.streams.channelMode.description')}
    />
  {/if}

  {#if ui.isMono}
    <div
      class="flex items-start gap-2 p-2.5 rounded-lg text-sm leading-relaxed bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-success)_30%,transparent)]"
    >
      <CircleCheck class="size-4 shrink-0 mt-0.5 text-[var(--color-success)]" />
      <span>{t('settings.audio.streams.channelMode.monoNoSelection')}</span>
    </div>
  {/if}

  {#if ui.showAnalysis}
    <button
      type="button"
      class="inline-flex items-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] hover:bg-[var(--color-base-300)] transition-colors disabled:opacity-50"
      onclick={() => onAnalyze(analyzeUrl)}
      disabled={!canAnalyze}
    >
      {#if isAnalyzing}
        <Loader2 class="size-4 animate-spin" />
        {t('settings.audio.streams.channelMode.analyzing')}
      {:else}
        <Search class="size-4" />
        {t('settings.audio.streams.channelMode.detectBest')}
      {/if}
    </button>

    {#if analysisResult}
      <div class="space-y-1 text-xs">
        {#each analysisResult.energy as ch (ch.channel)}
          <div class="flex items-center gap-2">
            <span class="w-20 text-[var(--color-base-content)]/70"
              >{ch.channel === 0
                ? t('settings.audio.streams.channelMode.energyLeft')
                : t('settings.audio.streams.channelMode.energyRight')}:</span
            >
            <div class="flex-1 h-2 bg-[var(--color-base-200)] rounded-full overflow-hidden">
              <div
                class="h-full rounded-full {(ch.channel === 0 ? 'left' : 'right') ===
                analysisResult.recommended
                  ? 'bg-[var(--color-success)]'
                  : 'bg-[var(--color-base-400)]'}"
                style:width="{Math.max(
                  MIN_BAR_PERCENT,
                  Math.min(
                    MAX_BAR_PERCENT,
                    (((ch.rmsDbfs ?? -DBFS_RANGE) + DBFS_RANGE) / DBFS_RANGE) * MAX_BAR_PERCENT
                  )
                )}%"
              ></div>
            </div>
            <span class="font-mono w-16 text-right">{ch.rmsDbfs.toFixed(1)} dBFS</span>
          </div>
        {/each}
        {#if analysisResult.recommended !== 'downmix'}
          <p class="text-[var(--color-success)] font-medium">
            {t('settings.audio.streams.channelMode.recommended', {
              channel:
                analysisResult.recommended === 'left'
                  ? t('settings.audio.streams.channelMode.energyLeft')
                  : t('settings.audio.streams.channelMode.energyRight'),
            })}
          </p>
        {/if}
      </div>
    {/if}

    {#if analysisError}
      <p class="text-xs text-[var(--color-error)]">
        {t('settings.audio.streams.channelMode.analyzeError')}: {analysisError}
      </p>
    {/if}

    {#if channelMode === 'downmix'}
      <div
        class="flex items-start gap-2 p-2.5 rounded-lg text-sm leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      >
        <AlertTriangle class="size-4 shrink-0 mt-0.5 text-[var(--color-warning)]" />
        <span>{t('settings.audio.streams.channelMode.downmixWarning')}</span>
      </div>
    {:else}
      <div
        class="flex items-start gap-2 p-2.5 rounded-lg text-sm leading-relaxed bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-success)_30%,transparent)]"
      >
        <CircleCheck class="size-4 shrink-0 mt-0.5 text-[var(--color-success)]" />
        <span>{t('settings.audio.streams.channelMode.singleChannelGood')}</span>
      </div>
    {/if}
  {/if}
</div>

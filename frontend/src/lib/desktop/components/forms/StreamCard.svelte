<!--
  Stream Card Component

  Purpose: One audio stream whose settings go into the settings form; the
  page's Save Changes bar persists them. The header is always visible and
  toggles the body: status pill, masked URL, badges, the enabled switch and
  delete, with the live stats and warnings below it. The body holds the stream's
  settings in groups and the diagnostics.

  Editing:
  - Every control writes only the key it changes, as the complete stream,
    through onChange. A name and a URL are written as typed and trimmed when the
    field is left. A connection change reconnects the stream when it is saved.
  - A URL that is not in the saved configuration is saved only after a
    successful test of exactly that URL; until then the field says so and the
    page blocks Save (the rules are in sourceRules.ts).
  - A name or URL error hides while that field is edited and shows again when it
    is left (also when a save or a collapse ends the edit without a blur). A
    problem another card caused shows at once, and so does the untested URL.

  Props:
  - stream: The stream as it is in the form
  - status: Connection status shown in the header
  - problem: What the page's save rules report for this stream
  - availableModels: Models the server lists for the source editors
  - modelOptions: Labels of the listed models, for the header summary
  - disabled: Blocks every change
  - disabledReason: Why it is disabled, shown beside delete (default: loading)
  - expanded: Whether the body is shown
  - onToggleExpanded: Called by the header button
  - onChange: Receives the complete changed stream
  - onUrlTested: Called with the URL a successful test was run for
  - onDelete: Called after the delete was confirmed

  @component
-->
<script lang="ts">
  import { getContext, untrack } from 'svelte';
  import { AlertTriangle, ChevronDown, Moon, Radio } from '@lucide/svelte';
  import { slide } from 'svelte/transition';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';
  import { maskUrlCredentials } from '$lib/utils/security';
  import {
    modelsFailed,
    modelsLoading,
    refreshModels,
    type BackendModel,
  } from '$lib/stores/models.svelte';
  import { acousticModelAvailability } from '$lib/stores/acousticModels.svelte';
  import { defaultModelSelection } from '$lib/utils/defaultModelSelection';
  import StatusPill, { type StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import TextInput from './TextInput.svelte';
  import ToggleField from './ToggleField.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import ModelToggleList from './ModelToggleList.svelte';
  import SourceModelBadges from './SourceModelBadges.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import AudioEqualizerSettings from '$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte';
  import SourceCard from '$lib/desktop/features/settings/components/SourceCard.svelte';
  import SettingsGroup from '$lib/desktop/features/settings/components/SettingsGroup.svelte';
  import { SETTINGS_GROUP_DISCLOSURE_CLASSES } from '$lib/desktop/features/settings/components/SettingsGroupLabel.svelte';
  import { savedEqualizer } from './sourceFields';
  import type { StreamProblem } from './sourceRules';
  import type {
    StreamConfig,
    EqualizerSettings,
    QuietHoursConfig,
    MediaMode,
    ChannelAnalysis,
  } from '$lib/stores/settings';
  import {
    defaultQuietHoursConfig,
    AUDIO_GAIN_MIN_DB,
    AUDIO_GAIN_MAX_DB,
  } from '$lib/stores/settings';
  import type { StreamHealthResponse } from './StreamManager.svelte';
  import StreamTestButton from './StreamTestButton.svelte';
  import StreamTimeline from './StreamTimeline.svelte';
  import StreamChannelControls from './StreamChannelControls.svelte';
  import {
    DEFAULT_SAMPLE_RATE,
    MAX_STREAM_NAME_LENGTH,
    MAX_STREAM_URL_LENGTH,
    streamTypeOptions,
    transportOptions,
    getMediaModeOptions,
    analyzeStreamChannels,
    detectStreamType,
    streamHasTransport,
    streamNameProblemMessage,
    streamUrlProblemMessage,
    toStreamType,
  } from './streamOptions';
  import { normalizeChannelMode } from './streamChannel';

  // Stream health status type
  export type StreamStatus =
    'connected' | 'connecting' | 'disabled' | 'error' | 'idle' | 'suppressed' | 'unknown';

  interface Props {
    stream: StreamConfig;
    status?: StreamStatus;
    problem?: StreamProblem;
    availableModels: BackendModel[];
    modelOptions: Array<{ value: string; label: string }>;
    disabled?: boolean;
    disabledReason?: string;
    expanded: boolean;
    onToggleExpanded: () => void;
    onChange: (_stream: StreamConfig) => void;
    onUrlTested: (_url: string) => void;
    onDelete: () => void;
  }

  let {
    stream,
    status = 'unknown',
    problem,
    availableModels,
    modelOptions,
    disabled = false,
    disabledReason,
    expanded,
    onToggleExpanded,
    onChange,
    onUrlTested,
    onDelete,
  }: Props = $props();

  const SECONDS_PER_MINUTE = 60;
  const SECONDS_PER_HOUR = 3600;
  const BYTES_PER_KIB = 1024;
  const BYTE_UNITS = ['B', 'KB', 'MB', 'GB'];
  const DEFAULT_TRANSPORT = 'tcp';
  const DEFAULT_MEDIA_MODE: MediaMode = 'full-stream';

  const cardId = generateId('stream');
  const nameInputId = `${cardId}-name`;
  const nameTextId = `${cardId}-stream-name`;
  const urlInputId = `${cardId}-url`;
  const modelsHeadingId = `${cardId}-models`;
  const equalizerHeadingId = `${cardId}-equalizer`;
  const diagnosticsId = `${cardId}-diagnostics`;

  function change(patch: Partial<StreamConfig>) {
    onChange({ ...stream, ...patch });
  }

  const acousticAvailability = $derived(acousticModelAvailability());
  let defaultModels = $derived(defaultModelSelection(acousticAvailability, availableModels));
  let selectedModels = $derived(stream.models ?? []);
  let listFailed = $derived(modelsFailed());
  let listLoading = $derived(modelsLoading());

  // ----- Health (live, from the manager's SSE) -----

  // The manager passes its $state object directly; reading its keys is reactive.
  const streamHealthMap = getContext<Record<string, StreamHealthResponse>>('streamHealth');
  let health = $derived(streamHealthMap[stream.url] ?? null);

  function formatBytes(bytes: number): string {
    // Guard <= 0 (not just === 0): a negative value would make Math.log return NaN.
    if (!bytes || bytes <= 0) return '--';
    const rawIndex = Math.floor(Math.log(bytes) / Math.log(BYTES_PER_KIB));
    const i = Math.min(Math.max(0, rawIndex), BYTE_UNITS.length - 1);
    // eslint-disable-next-line security/detect-object-injection -- index clamped to the array bounds
    return `${(bytes / Math.pow(BYTES_PER_KIB, i)).toFixed(1)} ${BYTE_UNITS[i]}`;
  }

  function formatRate(bytesPerSecond: number): string {
    if (!bytesPerSecond) return '--';
    return `${(bytesPerSecond / BYTES_PER_KIB).toFixed(1)} KB/s`;
  }

  function formatTimeAgo(seconds: number | undefined): string {
    if (seconds === undefined || seconds === null) return '--';
    if (seconds < SECONDS_PER_MINUTE) {
      return t('settings.audio.streams.ago.seconds', { count: Math.floor(seconds) });
    }
    if (seconds < SECONDS_PER_HOUR) {
      return t('settings.audio.streams.ago.minutes', {
        count: Math.floor(seconds / SECONDS_PER_MINUTE),
      });
    }
    return t('settings.audio.streams.ago.hours', {
      count: Math.floor(seconds / SECONDS_PER_HOUR),
    });
  }

  let lastDataAgo = $derived(formatTimeAgo(health?.time_since_data_seconds));
  let statsLine = $derived(
    t('settings.audio.streams.statsLine', {
      total: formatBytes(health?.total_bytes_received ?? 0),
      rate: formatRate(health?.bytes_per_second ?? 0),
      last: lastDataAgo,
    })
  );
  let statusMessage = $derived(health?.error ?? '');

  let connectionStatus = $derived.by(() => {
    if (!health) return 'unknown';
    if (health.process_state === 'circuit_open' || health.process_state === 'failed')
      return 'failed';
    if (health.process_state === 'backoff' || health.process_state === 'restarting')
      return 'degraded';
    if (health.is_healthy && health.is_receiving_data) return 'stable';
    return 'unknown';
  });

  const CONNECTION_STATUS_CLASS = new Map<string, string>([
    ['stable', 'text-[var(--text-success)]'],
    ['degraded', 'text-[var(--text-warning)]'],
    ['failed', 'text-[var(--text-error)]'],
  ]);

  // A stereo source still set to downmix. normalizeChannelMode treats an unset
  // or empty mode as downmix, which a bare `?? 'downmix'` misses.
  let showStereoWarning = $derived(
    (health?.source_channels ?? 0) >= 2 && normalizeChannelMode(stream.channelMode) === 'downmix'
  );

  // ----- Header -----

  const ICON_CLASSES = new Map<StreamStatus, string>([
    [
      'connected',
      'bg-[color-mix(in_srgb,var(--color-success)_20%,transparent)] text-[var(--text-success)] border-[color-mix(in_srgb,var(--color-success)_30%,transparent)]',
    ],
    [
      'connecting',
      'bg-[color-mix(in_srgb,var(--color-warning)_20%,transparent)] text-[var(--text-warning)] border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]',
    ],
    [
      'error',
      'bg-[color-mix(in_srgb,var(--color-error)_20%,transparent)] text-[var(--text-error)] border-[color-mix(in_srgb,var(--color-error)_30%,transparent)]',
    ],
    [
      'disabled',
      'bg-[var(--color-base-content)]/8 text-[var(--color-base-content)]/55 border-[var(--color-base-content)]/15',
    ],
    [
      'suppressed',
      'bg-[color-mix(in_srgb,var(--color-info)_20%,transparent)] text-[var(--text-info)] border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]',
    ],
    [
      'idle',
      'bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]/50 border-[var(--color-base-content)]/20',
    ],
    [
      'unknown',
      'bg-[var(--color-base-content)]/10 text-[var(--color-base-content)]/50 border-[var(--color-base-content)]/20',
    ],
  ]);

  const STATUS_VARIANTS = new Map<StreamStatus, StatusVariant>([
    ['connected', 'success'],
    ['connecting', 'warning'],
    ['error', 'error'],
    ['disabled', 'neutral'],
    ['suppressed', 'neutral'],
    ['idle', 'neutral'],
    ['unknown', 'neutral'],
  ]);

  function statusText(s: StreamStatus): string {
    switch (s) {
      case 'connected':
        return t('settings.audio.streams.status.connected');
      case 'connecting':
        return t('settings.audio.streams.status.connecting');
      case 'error':
        return t('settings.audio.streams.status.error');
      case 'disabled':
        return t('settings.audio.streams.disabled');
      case 'idle':
        return t('settings.audio.streams.status.idle');
      case 'suppressed':
        return t('settings.audio.streams.status.suppressed');
      default:
        return t('settings.audio.streams.status.unknown');
    }
  }

  let displayUrl = $derived(maskUrlCredentials(stream.url));
  let showTransport = $derived(streamHasTransport(stream.type) && Boolean(stream.transport));

  const BADGE_CLASS = 'px-2 py-0.5 rounded text-xs font-semibold';
  const INFO_BADGE_CLASS = 'bg-[var(--color-info)]/15 text-[var(--text-info)]';
  const NEUTRAL_BADGE_CLASS = 'bg-[var(--color-base-300)] text-[var(--color-base-content)]';

  // ----- Name -----

  // The field shows its problem once it was left, and hides it again while the
  // user edits it. A problem this field did not cause (a duplicate typed on
  // another card) shows at once, also on a card that opens with one.
  let nameTouched = $state(untrack(() => Boolean(problem?.name)));
  // Reactive, so an edit that ends without a blur (see the collapse and disable
  // effect below) shows a problem the edit had hidden.
  let nameEditing = $state(false);
  $effect(() => {
    if (problem?.name && !nameEditing) nameTouched = true;
  });
  let nameError = $derived(
    problem?.name && nameTouched ? streamNameProblemMessage(problem.name) : undefined
  );
  let hasProblem = $derived(Boolean(problem?.name || problem?.url));

  function handleNameInput(name: string) {
    nameEditing = true;
    nameTouched = false;
    if (name !== stream.name) change({ name });
  }

  function handleNameBlur(name: string) {
    nameEditing = false;
    nameTouched = true;
    const trimmed = name.trim();
    if (trimmed !== stream.name) change({ name: trimmed });
  }

  // ----- Connection (URL, type, transport, media mode, channel mode) -----

  // Like the name: the field shows its problem once it was left, hides it while
  // it is edited, and shows a problem another card caused at once. An untested
  // URL is shown always, since the test is the next step.
  let urlTouched = $state(untrack(() => Boolean(problem?.url && problem.url !== 'untested')));
  let urlEditing = $state(false);
  $effect(() => {
    if (problem?.url && !urlEditing) urlTouched = true;
  });
  let urlError = $derived.by(() => {
    if (!problem?.url) return undefined;
    return problem.url === 'untested' || urlTouched
      ? streamUrlProblemMessage(problem.url)
      : undefined;
  });

  // The result of the connection test of the current URL; any other URL clears it.
  let testResult = $state<{ sampleRate: number; channels: number } | null>(null);

  function handleUrlInput(url: string) {
    urlEditing = true;
    urlTouched = false;
    if (url === stream.url) return;
    // A URL that names its scheme picks the matching type, as in the add form.
    change({ url, type: url.includes('://') ? detectStreamType(url) : stream.type });
  }

  function handleUrlBlur(url: string) {
    urlEditing = false;
    urlTouched = true;
    const trimmed = url.trim();
    if (trimmed !== stream.url) change({ url: trimmed });
  }

  function setType(value: string | string[]) {
    const type = toStreamType(value);
    if (type && type !== stream.type) change({ type });
  }

  function setTransport(value: string | string[]) {
    if (value === 'tcp' || value === 'udp') change({ transport: value });
  }

  function setMediaMode(value: string | string[]) {
    if (value === 'auto' || value === 'audio-only' || value === 'full-stream') {
      change({ mediaMode: value });
    }
  }

  function handleTestResult(result: { sampleRate: number; channels: number } | null, url: string) {
    testResult = result;
    if (result) onUrlTested(url);
    else {
      analysisResult = null;
      analysisError = null;
    }
  }

  // Channel analysis of the URL; a newer run or a collapse discards a late result.
  let isAnalyzing = $state(false);
  let analysisResult = $state<ChannelAnalysis | null>(null);
  let analysisError = $state<string | null>(null);
  let analysisSeq = 0;

  async function analyzeChannels(url: string) {
    const seq = ++analysisSeq;
    isAnalyzing = true;
    analysisResult = null;
    analysisError = null;
    try {
      const result = await analyzeStreamChannels(url);
      if (seq !== analysisSeq || url.trim() !== stream.url.trim()) return;
      analysisResult = result;
      if (result.recommended && result.recommended !== 'downmix') {
        change({ channelMode: result.recommended });
      }
    } catch (err: unknown) {
      if (seq !== analysisSeq) return;
      analysisError = err instanceof Error ? err.message : String(err);
    } finally {
      if (seq === analysisSeq) isAnalyzing = false;
    }
  }

  $effect(() => {
    if (expanded) return;
    untrack(() => {
      analysisSeq++;
      isAnalyzing = false;
    });
  });

  // A collapsed or disabled card (a save is running) is not being edited: a field
  // can lose focus without a blur event when it is disabled or unmounted.
  $effect(() => {
    if (!expanded || disabled) {
      untrack(() => {
        nameEditing = false;
        urlEditing = false;
      });
    }
  });

  // A fresh test wins; otherwise the running stream's probe. 0 means not known yet.
  let detectedChannels = $derived(testResult?.channels ?? health?.source_channels ?? 0);
  let sourceSampleRate = $derived(testResult?.sampleRate ?? DEFAULT_SAMPLE_RATE);

  // ----- Equalizer -----

  let showEqualizer = $state(false);
  let equalizer = $derived<EqualizerSettings>(stream.equalizer ?? { enabled: false, filters: [] });

  function changeEqualizer(updated: EqualizerSettings) {
    change({ equalizer: savedEqualizer(updated) });
  }

  // ----- Quiet hours -----

  let quietHours = $derived<QuietHoursConfig>({ ...defaultQuietHoursConfig, ...stream.quietHours });

  function changeQuietHours(updated: QuietHoursConfig) {
    change({ quietHours: updated });
  }

  // ----- Diagnostics -----

  let showDiagnostics = $state(false);
</script>

<SourceCard
  name={stream.name}
  subtitle={displayUrl}
  icon={Radio}
  iconClass={ICON_CLASSES.get(status)}
  {expanded}
  {onToggleExpanded}
  deleteConfirm={t('settings.audio.streams.deleteConfirm', { name: stream.name })}
  {onDelete}
  {disabled}
  disabledReason={disabledReason ?? t('common.loading')}
>
  {#snippet nameAddon()}
    <StatusPill
      variant={STATUS_VARIANTS.get(status) ?? 'neutral'}
      label={statusText(status)}
      size="sm"
      pulse={status === 'connecting'}
    />
  {/snippet}

  {#snippet badges()}
    {#if hasProblem}
      <span class={cn(BADGE_CLASS, 'bg-[var(--color-error)]/15 text-[var(--text-error)]')}>
        {t('settings.audio.validation.needsAttention')}
      </span>
    {/if}
    {#if stream.quietHours?.enabled}
      <span
        class={cn(BADGE_CLASS, 'inline-flex items-center gap-1', NEUTRAL_BADGE_CLASS)}
        title={t('settings.audio.quietHours.enabledBadge')}
      >
        <Moon class="size-3" aria-hidden="true" />
        {t('settings.audio.quietHours.badge')}
      </span>
    {/if}
    {#if stream.gain}
      <span class={cn(BADGE_CLASS, 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]')}>
        {stream.gain > 0 ? '+' : ''}{stream.gain} dB
      </span>
    {/if}
    <SourceModelBadges
      {selectedModels}
      {defaultModels}
      {availableModels}
      {modelOptions}
      availability={acousticAvailability}
      {listLoading}
      {listFailed}
    />
  {/snippet}

  {#snippet actions()}
    <span id={nameTextId} class="sr-only">{stream.name}</span>
    <ToggleField
      variant="inline"
      label={t('settings.audio.streams.enabled')}
      value={stream.enabled}
      {disabled}
      describedBy={nameTextId}
      onUpdate={enabled => change({ enabled })}
    />
  {/snippet}

  {#snippet details()}
    {#if status === 'error' && statusMessage}
      <p class="text-xs text-[var(--text-error)]">{statusMessage}</p>
    {/if}
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <div class="flex flex-wrap items-center gap-1.5">
        <span class={cn(BADGE_CLASS, INFO_BADGE_CLASS)}>{stream.type.toUpperCase()}</span>
        {#if showTransport && stream.transport}
          <span class={cn(BADGE_CLASS, NEUTRAL_BADGE_CLASS)}>{stream.transport.toUpperCase()}</span>
        {/if}
        {#if stream.channelMode === 'left' || stream.channelMode === 'right'}
          <span
            class={cn(BADGE_CLASS, 'font-mono', INFO_BADGE_CLASS)}
            title={t(`settings.audio.streams.channelMode.${stream.channelMode}`)}
          >
            {stream.channelMode === 'left' ? 'L' : 'R'}
          </span>
        {/if}
        {#if stream.type === 'rtsp' && stream.mediaMode === 'auto'}
          <span class={cn(BADGE_CLASS, NEUTRAL_BADGE_CLASS)}>
            {t('settings.audio.streams.mediaMode.auto')}
          </span>
        {:else if stream.type === 'rtsp' && stream.mediaMode === 'audio-only'}
          <span class={cn(BADGE_CLASS, NEUTRAL_BADGE_CLASS)}>
            {t('settings.audio.streams.mediaMode.audioOnly')}
          </span>
        {/if}
      </div>
      <p class="text-xs text-muted">{statsLine}</p>
    </div>
    {#if showStereoWarning}
      <p class="flex items-center gap-1.5 text-xs text-[var(--text-warning)]">
        <AlertTriangle class="size-3.5 shrink-0" aria-hidden="true" />
        {t('settings.audio.streams.stereoWarning.short')}
      </p>
    {/if}
  {/snippet}

  <!-- Name and level -->
  <div class="grid grid-cols-1 gap-x-8 gap-y-4 py-4 md:grid-cols-2">
    <SettingsGroup
      title={t('settings.audio.streams.nameLabel')}
      labelFor={nameInputId}
      required
      class="space-y-1.5"
    >
      <TextInput
        id={nameInputId}
        value={stream.name}
        placeholder={t('settings.audio.streams.namePlaceholder')}
        maxlength={MAX_STREAM_NAME_LENGTH}
        aria-required="true"
        {disabled}
        error={nameError}
        oninput={handleNameInput}
        onblur={handleNameBlur}
      />
    </SettingsGroup>

    <SettingsGroup
      title={t('settings.audio.soundCards.levelTitle')}
      headingId="{cardId}-level"
      class="space-y-1.5"
    >
      <InlineSlider
        label={t('settings.audio.soundCards.gainLabel')}
        value={stream.gain ?? 0}
        updateOn="change"
        onUpdate={value => change({ gain: value })}
        min={AUDIO_GAIN_MIN_DB}
        max={AUDIO_GAIN_MAX_DB}
        step={1}
        unit=" dB"
        {disabled}
      />
    </SettingsGroup>
  </div>

  <!-- Connection -->
  <SettingsGroup
    title={t('settings.audio.streams.connectionTitle')}
    headingId="{cardId}-connection"
    class="py-4"
    titleVariant="group"
  >
    <div class="space-y-4">
      <SettingsGroup
        title={t('settings.audio.streams.urlLabel')}
        labelFor={urlInputId}
        required
        class="space-y-1.5"
      >
        <TextInput
          id={urlInputId}
          value={stream.url}
          placeholder="rtsp://user:password@host:port/path"
          maxlength={MAX_STREAM_URL_LENGTH}
          aria-required="true"
          {disabled}
          error={urlError}
          oninput={handleUrlInput}
          onblur={handleUrlBlur}
        />
      </SettingsGroup>

      <StreamTestButton
        url={stream.url}
        models={availableModels}
        {selectedModels}
        {disabled}
        onResult={handleTestResult}
      />

      <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <SelectDropdown
          value={stream.type}
          label={t('settings.audio.streams.typeLabel')}
          options={streamTypeOptions}
          {disabled}
          onChange={setType}
          groupBy={false}
          menuSize="sm"
        />
        {#if streamHasTransport(stream.type)}
          <SelectDropdown
            value={stream.transport ?? DEFAULT_TRANSPORT}
            label={t('settings.audio.streams.transportLabel')}
            options={transportOptions}
            {disabled}
            onChange={setTransport}
            groupBy={false}
            menuSize="sm"
          />
        {/if}
      </div>

      <!-- RTSP media mode: audio-only handshake, full stream, or auto fallback -->
      {#if stream.type === 'rtsp'}
        <SelectDropdown
          value={stream.mediaMode || DEFAULT_MEDIA_MODE}
          label={t('settings.audio.streams.mediaModeLabel')}
          helpText={t('settings.audio.streams.mediaModeHelp')}
          options={getMediaModeOptions()}
          {disabled}
          onChange={setMediaMode}
          groupBy={false}
          menuSize="sm"
        />
      {/if}

      <StreamChannelControls
        channelMode={normalizeChannelMode(stream.channelMode)}
        channels={detectedChannels}
        sampleRate={testResult?.sampleRate}
        analyzeUrl={stream.url}
        {isAnalyzing}
        {analysisResult}
        {analysisError}
        {disabled}
        onChange={mode => change({ channelMode: mode })}
        onAnalyze={url => void analyzeChannels(url)}
      />
    </div>
  </SettingsGroup>

  <!-- Classifiers -->
  <SettingsGroup
    title={t('settings.audio.soundCards.modelLabel')}
    headingId={modelsHeadingId}
    class="py-4"
    titleVariant="group"
  >
    <ModelToggleList
      models={availableModels}
      {selectedModels}
      {defaultModels}
      sourceName={stream.name}
      {sourceSampleRate}
      isStream
      loading={listLoading}
      failed={listFailed}
      availability={acousticAvailability}
      labelledBy={modelsHeadingId}
      {disabled}
      onChange={models => change({ models })}
      onRetry={() => void refreshModels()}
    />
  </SettingsGroup>

  <!-- Audio filters (collapsed by default) -->
  <SettingsGroup headingId={equalizerHeadingId} class="space-y-0 py-4">
    {#snippet heading()}
      <button
        id={equalizerHeadingId}
        type="button"
        class={SETTINGS_GROUP_DISCLOSURE_CLASSES}
        aria-expanded={showEqualizer}
        onclick={() => (showEqualizer = !showEqualizer)}
      >
        <ChevronDown
          class={cn(
            'size-4 transition-transform duration-200 motion-reduce:transition-none',
            showEqualizer && 'rotate-180'
          )}
          aria-hidden="true"
        />
        {t('settings.audio.audioFilters.title')}
      </button>
      <div class="flex items-center gap-3">
        {#if equalizer.enabled}
          <span class={cn(BADGE_CLASS, 'bg-[var(--color-primary)]/15 text-[var(--text-primary)]')}>
            {t('common.enabled')}
          </span>
        {/if}
      </div>
    {/snippet}
    {#if showEqualizer}
      <div class="mt-3" transition:slide={{ duration: 150 }}>
        <!-- No sampleRate: a stream's EQ chain runs at the pipeline output rate,
             which is the server's configured rate (the preview default) for
             every stream except a bat stream with a higher native rate. The
             probed stream rate is not that rate, so it is not sent. -->
        <AudioEqualizerSettings
          equalizerSettings={equalizer}
          {disabled}
          onUpdate={changeEqualizer}
        />
      </div>
    {/if}
  </SettingsGroup>

  <!-- Quiet hours -->
  <SettingsGroup
    title={t('settings.audio.quietHours.title')}
    headingId="{cardId}-quiet"
    class="py-4"
    titleVariant="group"
  >
    <QuietHoursEditor
      config={quietHours}
      onChange={changeQuietHours}
      {disabled}
      idPrefix="{cardId}-qh"
      enableLabel={t('settings.audio.quietHours.enableLabel')}
    />
  </SettingsGroup>

  <!-- Diagnostics (collapsed by default) -->
  <SettingsGroup class="space-y-0 py-4">
    {#snippet heading()}
      <button
        type="button"
        class={SETTINGS_GROUP_DISCLOSURE_CLASSES}
        aria-expanded={showDiagnostics}
        aria-controls={diagnosticsId}
        onclick={() => (showDiagnostics = !showDiagnostics)}
      >
        <ChevronDown
          class={cn(
            'size-4 transition-transform duration-200 motion-reduce:transition-none',
            showDiagnostics && 'rotate-180'
          )}
          aria-hidden="true"
        />
        {t('settings.audio.streams.diagnostics.title')}
      </button>
    {/snippet}
    {#if showDiagnostics}
      <div id={diagnosticsId} class="mt-3 space-y-4" transition:slide={{ duration: 150 }}>
        <dl class="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
          <div class="flex gap-2">
            <dt class="font-medium text-muted">
              {t('settings.audio.streams.diagnostics.processState')}
            </dt>
            <dd
              class={cn(
                health?.process_state === 'circuit_open' ||
                  health?.process_state === 'failed' ||
                  health?.process_state === 'stopped'
                  ? 'text-[var(--text-error)]'
                  : 'text-[var(--color-base-content)]'
              )}
            >
              {health?.process_state ?? t('common.unknown')}
            </dd>
          </div>
          <div class="flex gap-2">
            <dt class="font-medium text-muted">
              {t('settings.audio.streams.diagnostics.lastData')}
            </dt>
            <dd class="text-[var(--color-base-content)]">{lastDataAgo}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="font-medium text-muted">
              {t('settings.audio.streams.diagnostics.restartCount')}
            </dt>
            <dd
              class={cn(
                (health?.restart_count ?? 0) > 0
                  ? 'text-[var(--text-warning)]'
                  : 'text-[var(--color-base-content)]'
              )}
            >
              {health?.restart_count ?? 0}
            </dd>
          </div>
          <div class="flex gap-2">
            <dt class="font-medium text-muted">
              {t('settings.audio.streams.diagnostics.connection')}
            </dt>
            <dd
              class={CONNECTION_STATUS_CLASS.get(connectionStatus) ??
                'text-[var(--color-base-content)]'}
            >
              {t(`settings.audio.streams.connectionStatus.${connectionStatus}`)}
            </dd>
          </div>
        </dl>

        {#if health?.state_history?.length || health?.error_history?.length}
          <div class="border-t border-[var(--border-100)] pt-4">
            <p class="mb-3 text-xs font-medium text-muted">
              {t('settings.audio.streams.diagnostics.stateErrorHistory')}
            </p>
            <StreamTimeline
              stateHistory={health?.state_history}
              errorHistory={health?.error_history}
            />
          </div>
        {/if}
      </div>
    {/if}
  </SettingsGroup>
</SourceCard>

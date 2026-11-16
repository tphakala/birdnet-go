<!--
  Stream Card Component

  Purpose: One audio stream whose settings save as they change. The header is
  always visible and toggles the body: status pill, masked URL, badges, the
  enabled switch and delete, with the live stats and warnings below it. The
  body holds the stream's settings in groups, each with its own save status,
  and the diagnostics.

  Saving:
  - Enabled and model switches, selects of the equalizer and quiet hours:
    committed at once.
  - Name: a local draft, committed on blur or Enter; Escape restores it. An
    empty or duplicate name is not committed and the field says why. A rename
    reconnects the stream.
  - Connection (URL, type, transport, media mode, channel mode): a local draft
    with a scoped "Apply and reconnect", since a change reconnects the stream.
    A changed URL must be valid, unused by another stream and tested first;
    until then Apply is blocked and says why. A failed apply keeps the draft.
  - Gain, equalizer numbers, quiet hours times and offsets: committed after the
    auto-save debounce (gain on release).

  Props:
  - stream: The saved stream
  - index: Position of the stream in streams
  - streams: Every stream, for the duplicate checks
  - rowId: UI-only id of the row, part of every field key
  - status: Connection status shown in the header
  - availableModels: Models the server lists for the source editors
  - modelOptions: Labels of the listed models, for the header summary
  - disabled: Blocks every change
  - expanded: Whether the body is shown
  - onToggleExpanded: Called by the header button
  - onCommit: Receives the complete changed stream and the auto-save options
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
  import {
    AUTOSAVE_DEBOUNCE_MS,
    autoSaveStatus,
    type CommitOptions,
  } from '$lib/stores/settingsAutoSave.svelte';
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
  import AutoSaveStatus from '$lib/desktop/features/settings/components/AutoSaveStatus.svelte';
  import SourceCard from '$lib/desktop/features/settings/components/SourceCard.svelte';
  import SettingsGroup from '$lib/desktop/features/settings/components/SettingsGroup.svelte';
  import CommitTextField from '$lib/desktop/features/settings/components/CommitTextField.svelte';
  import ScopedApplyGroup from '$lib/desktop/features/settings/components/ScopedApplyGroup.svelte';
  import { ApplyDraft } from '$lib/desktop/features/settings/components/applyDraft.svelte';
  import {
    STREAMS_FIELD_PREFIX,
    isContinuousEqualizerChange,
    isContinuousQuietHoursChange,
    savedEqualizer,
    sourceField,
    type SourceFieldPart,
  } from './sourceFields';
  import type {
    StreamConfig,
    StreamType,
    EqualizerSettings,
    QuietHoursConfig,
    ChannelMode,
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
    isStreamNameTooLong,
    isValidStreamUrl,
    streamHasTransport,
    streamNameTooLongMessage,
    toStreamType,
  } from './streamOptions';
  import { normalizeChannelMode } from './streamChannel';

  // Stream health status type
  export type StreamStatus =
    'connected' | 'connecting' | 'disabled' | 'error' | 'idle' | 'suppressed' | 'unknown';

  interface Props {
    stream: StreamConfig;
    index: number;
    streams: StreamConfig[];
    rowId: string;
    status?: StreamStatus;
    availableModels: BackendModel[];
    modelOptions: Array<{ value: string; label: string }>;
    disabled?: boolean;
    expanded: boolean;
    onToggleExpanded: () => void;
    onCommit: (_stream: StreamConfig, _options: CommitOptions) => void;
    onDelete: () => void;
  }

  let {
    stream,
    index,
    streams,
    rowId,
    status = 'unknown',
    availableModels,
    modelOptions,
    disabled = false,
    expanded,
    onToggleExpanded,
    onCommit,
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

  function field(part: SourceFieldPart): string {
    return sourceField(STREAMS_FIELD_PREFIX, rowId, part);
  }

  function statusId(part: SourceFieldPart): string {
    return `${cardId}-${part}-status`;
  }

  function commit(change: Partial<StreamConfig>, part: SourceFieldPart, debounce = false) {
    onCommit(
      { ...stream, ...change },
      debounce ? { field: field(part), debounceMs: AUTOSAVE_DEBOUNCE_MS } : { field: field(part) }
    );
  }

  /** aria-describedby for a control: its status message while that shows an error. */
  function errorDescription(part: SourceFieldPart): string | undefined {
    return autoSaveStatus(field(part)).state === 'error' ? statusId(part) : undefined;
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

  function validateName(name: string): string | null {
    if (!name) return t('settings.audio.streams.errors.nameRequired');
    if (isStreamNameTooLong(name)) return streamNameTooLongMessage();
    const lower = name.toLowerCase();
    if (streams.some((s, i) => i !== index && s.name.toLowerCase() === lower)) {
      return t('settings.audio.streams.errors.duplicateName');
    }
    return null;
  }

  // ----- Connection (applied together, the stream reconnects) -----

  interface ConnectionDraft {
    url: string;
    type: StreamType;
    transport: 'tcp' | 'udp';
    mediaMode: MediaMode;
    channelMode: ChannelMode;
  }

  const connection = new ApplyDraft<ConnectionDraft>(
    () => ({
      url: stream.url,
      type: stream.type,
      transport: stream.transport ?? DEFAULT_TRANSPORT,
      // An empty or unset media mode is the full-stream default.
      mediaMode: stream.mediaMode || DEFAULT_MEDIA_MODE,
      channelMode: normalizeChannelMode(stream.channelMode),
    }),
    untrack(() => field('connection'))
  );

  /** The stream with the connection applied; fields a type does not use are left out. */
  function withConnection(value: ConnectionDraft): StreamConfig {
    const next: StreamConfig = {
      ...stream,
      url: value.url.trim(),
      type: value.type,
      channelMode: value.channelMode,
    };
    delete next.transport;
    delete next.mediaMode;
    if (streamHasTransport(value.type)) next.transport = value.transport;
    if (value.type === 'rtsp') next.mediaMode = value.mediaMode;
    return next;
  }

  let draftUrl = $derived(connection.draft.url.trim());
  let urlChanged = $derived(draftUrl !== stream.url);

  // The connection test of the current draft URL; any other URL needs a new test.
  let testResult = $state<{ sampleRate: number; channels: number } | null>(null);
  let testedUrl = $state<string | null>(null);
  let needsTest = $derived(urlChanged && testedUrl !== draftUrl);

  let typeChanged = $derived(connection.draft.type !== stream.type);

  // The URL must fit the type whenever either differs from the saved stream, so
  // a type switch alone cannot apply a URL of another scheme.
  let urlProblem = $derived.by(() => {
    if (!urlChanged && !typeChanged) return null;
    if (!draftUrl) return t('settings.audio.streams.errors.urlRequired');
    if (!isValidStreamUrl(draftUrl, connection.draft.type)) {
      return t('settings.audio.streams.errors.invalidUrl');
    }
    if (urlChanged && streams.some((s, i) => i !== index && s.url === draftUrl)) {
      return t('settings.audio.streams.errors.duplicate');
    }
    return null;
  });

  // The field shows its problem once it was left, the Apply row at once.
  let urlTouched = $state(false);
  let applyBlocked = $derived(urlProblem !== null || needsTest);
  let connectionHint = $derived(
    urlProblem ??
      (needsTest
        ? t('settings.audio.streams.testBeforeApply')
        : t('settings.audio.streams.connectionApplyHint'))
  );

  function setUrl(value: string) {
    // A URL that names its scheme picks the matching type, as in the add form.
    connection.set(
      value.includes('://') ? { url: value, type: detectStreamType(value) } : { url: value }
    );
  }

  function setType(value: string | string[]) {
    const type = toStreamType(value);
    if (type) connection.set({ type });
  }

  function setTransport(value: string | string[]) {
    if (value === 'tcp' || value === 'udp') connection.set({ transport: value });
  }

  function setMediaMode(value: string | string[]) {
    if (value === 'auto' || value === 'audio-only' || value === 'full-stream') {
      connection.set({ mediaMode: value });
    }
  }

  function applyConnection(value: ConnectionDraft) {
    if (applyBlocked) return;
    onCommit(withConnection(value), { field: field('connection') });
  }

  function handleTestResult(result: { sampleRate: number; channels: number } | null) {
    testResult = result;
    testedUrl = result ? draftUrl : null;
    if (!result) {
      analysisResult = null;
      analysisError = null;
    }
  }

  // Channel analysis of the draft URL; a newer run or a collapse discards a late result.
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
      if (seq !== analysisSeq || url.trim() !== (draftUrl || stream.url).trim()) return;
      analysisResult = result;
      if (result.recommended && result.recommended !== 'downmix') {
        connection.set({ channelMode: result.recommended });
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

  // A fresh test wins; otherwise the running stream's probe. 0 means not known yet.
  let detectedChannels = $derived(testResult?.channels ?? health?.source_channels ?? 0);
  let sourceSampleRate = $derived(testResult?.sampleRate ?? DEFAULT_SAMPLE_RATE);

  // ----- Equalizer -----

  let showEqualizer = $state(false);
  let equalizer = $derived<EqualizerSettings>(stream.equalizer ?? { enabled: false, filters: [] });

  function commitEqualizer(updated: EqualizerSettings) {
    commit(
      { equalizer: savedEqualizer(updated) },
      'equalizer',
      isContinuousEqualizerChange(updated, equalizer)
    );
  }

  // ----- Quiet hours -----

  let quietHours = $derived<QuietHoursConfig>({ ...defaultQuietHoursConfig, ...stream.quietHours });

  function commitQuietHours(updated: QuietHoursConfig) {
    commit(
      { quietHours: updated },
      'quietHours',
      isContinuousQuietHoursChange(updated, quietHours)
    );
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
  disabledReason={t('common.loading')}
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
      describedBy={[nameTextId, errorDescription('enabled')].filter(Boolean).join(' ')}
      onUpdate={enabled => commit({ enabled }, 'enabled')}
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
    <AutoSaveStatus field={field('enabled')} id={statusId('enabled')} />
  {/snippet}

  <!-- Name and level -->
  <div class="grid grid-cols-1 gap-x-8 gap-y-4 py-4 md:grid-cols-2">
    <SettingsGroup
      title={t('settings.audio.streams.nameLabel')}
      labelFor={nameInputId}
      required
      field={field('name')}
      statusId={statusId('name')}
      class="space-y-1.5"
    >
      <CommitTextField
        id={nameInputId}
        value={stream.name}
        validate={validateName}
        onCommit={name => commit({ name }, 'name')}
        placeholder={t('settings.audio.streams.namePlaceholder')}
        maxlength={MAX_STREAM_NAME_LENGTH}
        required
        {disabled}
        describedBy={errorDescription('name')}
      />
    </SettingsGroup>

    <SettingsGroup
      title={t('settings.audio.soundCards.levelTitle')}
      headingId="{cardId}-level"
      field={field('gain')}
      statusId={statusId('gain')}
      class="space-y-1.5"
    >
      <InlineSlider
        label={t('settings.audio.soundCards.gainLabel')}
        value={stream.gain ?? 0}
        updateOn="change"
        onUpdate={value => commit({ gain: value }, 'gain', true)}
        min={AUDIO_GAIN_MIN_DB}
        max={AUDIO_GAIN_MAX_DB}
        step={1}
        unit=" dB"
        {disabled}
      />
    </SettingsGroup>
  </div>

  <!-- Connection: a draft with a scoped apply -->
  <SettingsGroup
    title={t('settings.audio.streams.connectionTitle')}
    headingId="{cardId}-connection"
    field={field('connection')}
    statusId={statusId('connection')}
    class="py-4"
  >
    <ScopedApplyGroup
      draft={connection}
      onApply={applyConnection}
      hint={connectionHint}
      failedHint={t('settings.audio.streams.connectionApplyFailed')}
      applyLabel={t('settings.audio.streams.applyConnection')}
      applyingLabel={t('settings.audio.soundCards.applying')}
      statusId={statusId('connection')}
      disabled={disabled || applyBlocked}
    >
      <div class="space-y-4">
        <TextInput
          id={urlInputId}
          value={connection.draft.url}
          label={t('settings.audio.streams.urlLabel')}
          placeholder="rtsp://user:password@host:port/path"
          maxlength={MAX_STREAM_URL_LENGTH}
          required
          {disabled}
          error={urlTouched && urlProblem ? urlProblem : undefined}
          oninput={setUrl}
          onblur={() => (urlTouched = true)}
        />

        <StreamTestButton
          url={connection.draft.url}
          models={availableModels}
          {selectedModels}
          {disabled}
          onResult={handleTestResult}
        />

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <SelectDropdown
            value={connection.draft.type}
            label={t('settings.audio.streams.typeLabel')}
            options={streamTypeOptions}
            {disabled}
            onChange={setType}
            groupBy={false}
            menuSize="sm"
          />
          {#if streamHasTransport(connection.draft.type)}
            <SelectDropdown
              value={connection.draft.transport}
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
        {#if connection.draft.type === 'rtsp'}
          <SelectDropdown
            value={connection.draft.mediaMode}
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
          channelMode={connection.draft.channelMode}
          channels={detectedChannels}
          sampleRate={testResult?.sampleRate}
          analyzeUrl={draftUrl || stream.url}
          {isAnalyzing}
          {analysisResult}
          {analysisError}
          {disabled}
          onChange={mode => connection.set({ channelMode: mode })}
          onAnalyze={url => void analyzeChannels(url)}
        />
      </div>
    </ScopedApplyGroup>
  </SettingsGroup>

  <!-- Classifiers -->
  <SettingsGroup
    title={t('settings.audio.soundCards.modelLabel')}
    headingId={modelsHeadingId}
    field={field('models')}
    statusId={statusId('models')}
    class="py-4"
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
      onChange={models => commit({ models }, 'models')}
      onRetry={() => void refreshModels()}
    />
  </SettingsGroup>

  <!-- Audio filters (collapsed by default) -->
  <SettingsGroup headingId={equalizerHeadingId} class="space-y-0 py-4">
    {#snippet heading()}
      <button
        id={equalizerHeadingId}
        type="button"
        class="flex items-center gap-2 rounded text-sm font-semibold text-[var(--color-base-content)] transition-colors hover:text-[var(--text-primary)]"
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
        <AutoSaveStatus field={field('equalizer')} id={statusId('equalizer')} />
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
          onUpdate={commitEqualizer}
        />
      </div>
    {/if}
  </SettingsGroup>

  <!-- Quiet hours -->
  <SettingsGroup
    title={t('settings.audio.quietHours.title')}
    headingId="{cardId}-quiet"
    field={field('quietHours')}
    statusId={statusId('quietHours')}
    class="py-4"
  >
    <QuietHoursEditor
      config={quietHours}
      onChange={commitQuietHours}
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
        class="flex items-center gap-2 rounded text-sm font-semibold text-[var(--color-base-content)] transition-colors hover:text-[var(--text-primary)]"
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

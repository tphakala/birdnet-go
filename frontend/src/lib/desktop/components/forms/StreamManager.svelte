<!--
  Stream Manager Component

  Purpose: Manage the audio streams of a tab that saves as it changes: stream
  cards with live health, an add form that saves the new stream at once,
  delete, a status summary bar and empty state guidance.

  Every change goes out through onUpdateStreams as the whole streams array with
  the auto-save options of the control that made it. Rows carry a UI-only id
  (never persisted) that keys the cards, their field keys and their expanded
  state; a length change the manager did not make gives every row a fresh id.

  Features:
  - Stream cards: one stream opens by default, several start closed
  - Add new streams with name, URL (tested first), type and protocol settings
  - Real-time health status via SSE
  - Summary bar showing healthy/unhealthy counts
  - Empty state with guidance
  - Auto-detect stream type from URL

  @component
-->
<script lang="ts">
  import { onMount, onDestroy, setContext, tick, untrack } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { Plus, Radio, RefreshCw } from '@lucide/svelte';
  import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { sanitizeUrlForComparison } from '$lib/utils/security';
  import { generateId } from '$lib/utils/uuid';
  import { quietHoursStore } from '$lib/stores/quietHours.svelte';
  import {
    getAvailableModels,
    watchModels,
    modelsLoading,
    modelsFailed,
    refreshModels,
  } from '$lib/stores/models.svelte';
  import { autoSaveStatus, type CommitOptions } from '$lib/stores/settingsAutoSave.svelte';
  import AutoSaveStatus from '$lib/desktop/features/settings/components/AutoSaveStatus.svelte';
  import { modelLabel } from '$lib/utils/modelLabel';
  import { STREAMS_LIST_FIELD, RowRollback } from './sourceFields';
  import {
    acousticModelAvailability,
    subscribeAcousticModels,
  } from '$lib/stores/acousticModels.svelte';
  import { defaultModelSelection } from '$lib/utils/defaultModelSelection';
  import StreamCard, { type StreamStatus } from './StreamCard.svelte';
  import InlineSlider from './InlineSlider.svelte';
  import ModelToggleList from './ModelToggleList.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import EmptyState from '$lib/desktop/features/settings/components/EmptyState.svelte';
  import SelectDropdown from './SelectDropdown.svelte';
  import TextInput from './TextInput.svelte';
  import QuietHoursEditor from './QuietHoursEditor.svelte';
  import type {
    StreamConfig,
    StreamType,
    QuietHoursConfig,
    ChannelMode,
  } from '$lib/stores/settings';
  import type { ChannelAnalysis } from '$lib/stores/settings';
  import {
    defaultQuietHoursConfig,
    AUDIO_GAIN_MIN_DB,
    AUDIO_GAIN_MAX_DB,
  } from '$lib/stores/settings';
  import StreamTestButton from './StreamTestButton.svelte';
  import StreamChannelControls from './StreamChannelControls.svelte';
  import {
    DEFAULT_SAMPLE_RATE,
    MAX_STREAM_NAME_LENGTH,
    streamTypeOptions,
    transportOptions,
    analyzeStreamChannels,
    detectStreamType,
    isStreamNameTooLong,
    isValidStreamUrl,
    streamHasTransport,
    streamNameTooLongMessage,
    toStreamType,
  } from './streamOptions';

  const logger = loggers.audio;

  const availableModels = $derived(getAvailableModels());
  const acousticAvailability = $derived(acousticModelAvailability());

  $effect(() => {
    return watchModels();
  });

  $effect(() => {
    return subscribeAcousticModels();
  });

  // Pre-selection for a stream without an explicit list: the classifier's
  // default targets when known, nothing at N=0, the legacy BirdNET pick otherwise.
  const defaultModels = $derived(defaultModelSelection(acousticAvailability, availableModels));

  const modelOptions = $derived(availableModels.map(m => ({ value: m.id, label: modelLabel(m) })));

  const ROW_ID_PREFIX = 'stream-row';
  /** Id of the add form's save status, which explains why Cancel is blocked while saving. */
  const ADD_SAVING_STATUS_ID = 'new-stream-saving-status';
  /** How many lists the manager remembers sending or holding, to tell its own from foreign ones. */
  const MAX_KNOWN_LISTS = 16;
  /** Delay before the health of a newly added stream is fetched, so it has started. */
  const HEALTH_REFRESH_AFTER_ADD_MS = 1000;
  let healthRefreshTimer: ReturnType<typeof setTimeout> | undefined;

  // Error context from FFmpeg error parsing (matching backend ErrorContextResponse)
  export interface ErrorContext {
    error_type: string;
    primary_message: string;
    user_facing_msg: string;
    troubleshooting_steps?: string[];
    timestamp: string;
    // Technical details (optional)
    target_host?: string;
    target_port?: number;
    timeout_duration?: string;
    http_status?: number;
    rtsp_method?: string;
    // Action recommendations
    should_open_circuit: boolean;
    should_restart: boolean;
  }

  // State transition event (matching backend StateTransitionResponse)
  export interface StateTransition {
    from_state: string;
    to_state: string;
    timestamp: string;
    reason?: string;
  }

  // Stream health response types (matching backend API)
  export interface StreamHealthResponse {
    name?: string; // Stream name for unambiguous matching
    type?: string; // Stream type from config
    url: string;
    is_healthy: boolean;
    process_state: string;
    last_data_received: string | null;
    time_since_data_seconds?: number;
    restart_count: number;
    error?: string;
    total_bytes_received: number;
    bytes_per_second: number;
    is_receiving_data: boolean;
    source_channels?: number; // Detected channel count (0 or absent if unknown)
    // Error diagnostics
    last_error_context?: ErrorContext | null;
    error_history?: ErrorContext[];
    // State history for debugging
    state_history?: StateTransition[];
  }

  interface Props {
    streams: StreamConfig[];
    disabled?: boolean;
    onUpdateStreams: (_streams: StreamConfig[], _options: CommitOptions) => void;
  }

  let { streams = [], disabled = false, onUpdateStreams }: Props = $props();

  // Stream health state - using $state object passed via context for reactivity
  // In Svelte 5, passing a $state object to context allows children to access the same reactive proxy
  // IMPORTANT: Pass the object directly, not a getter. Never reassign - only mutate properties.
  let streamHealth = $state<Record<string, StreamHealthResponse>>({});

  // Provide the state object via context so children can access it reactively
  // Pass the object directly - children will see mutations to its properties
  setContext('streamHealth', streamHealth);

  let healthLoading = $state(false);

  // Add new stream state
  let showAddForm = $state(false);
  let newName = $state('');
  let newUrl = $state('');
  let newTransport = $state<'tcp' | 'udp'>('tcp');
  let newStreamType = $state<StreamType>('rtsp');
  let newGain = $state(0);
  let newModels = $state<string[]>([]);
  let newQuietHours = $state<QuietHoursConfig>({ ...defaultQuietHoursConfig });
  let newChannelMode = $state<ChannelMode>('downmix');
  let nameError = $state<string | null>(null);
  let urlError = $state<string | null>(null);
  let newTestResult = $state<{ sampleRate: number; channels: number } | null>(null);
  // The URL the test result belongs to; any other URL needs a new test.
  let newTestedUrl = $state<string | null>(null);
  let newNeedsTest = $derived(newUrl.trim() !== '' && newTestedUrl !== newUrl.trim());
  let newSourceSampleRate = $derived(newTestResult?.sampleRate ?? DEFAULT_SAMPLE_RATE);
  // Detected channel count for a new stream comes only from a stream test
  // (0 = not yet tested). Drives the adaptive channel controls.
  let newDetectedChannels = $derived(newTestResult?.channels ?? 0);
  let newDetectedSampleRate = $derived(newTestResult?.sampleRate);
  let isAnalyzing = $state(false);
  let analysisResult = $state<ChannelAnalysis | null>(null);
  let analysisError = $state<string | null>(null);
  let analysisSeq = 0;

  // SSE connection for real-time health updates
  let eventSource: ReconnectingEventSource | null = null;

  // Show transport dropdown only for RTSP and RTMP
  let showTransportInAdd = $derived(streamHasTransport(newStreamType));

  // Summary stats
  let healthySummary = $derived.by(() => {
    let healthy = 0;
    let unhealthy = 0;
    let unknown = 0;
    let disabledCount = 0;

    streams.forEach(stream => {
      if (!stream.enabled) {
        disabledCount++;
        return;
      }

      const health = streamHealth[stream.url];
      if (!health) {
        unknown++;
      } else if (health.is_healthy && health.is_receiving_data) {
        healthy++;
      } else {
        unhealthy++;
      }
    });

    return { healthy, unhealthy, unknown, disabled: disabledCount, total: streams.length };
  });

  // Check if a specific stream is suppressed by quiet hours.
  // The backend returns sanitized URLs (credentials stripped), so we compare using sanitized form.
  function isStreamSuppressed(url: string, stream: StreamConfig): boolean {
    const qhStatus = quietHoursStore.status;
    if (!stream.quietHours?.enabled || !qhStatus?.suppressedStreams) return false;
    const sanitized = sanitizeUrlForComparison(url);
    // eslint-disable-next-line security/detect-object-injection -- sanitized URL from internal sanitizeUrlForComparison, not user input
    return qhStatus.suppressedStreams[sanitized] === true;
  }

  // Convert backend process state to UI status
  function getStreamStatus(url: string, stream: StreamConfig): StreamStatus {
    if (!stream.enabled) {
      return 'disabled';
    }

    // eslint-disable-next-line security/detect-object-injection -- URL from validated stream config, not user input
    const health = streamHealth[url];
    if (!health) {
      // No health data means the stream was removed from the active map.
      // Check per-stream suppression state rather than global anyActive flag.
      if (isStreamSuppressed(url, stream)) {
        return 'suppressed';
      }
      return 'unknown';
    }

    const state = health.process_state.toLowerCase();

    if (state === 'running' && health.is_healthy && health.is_receiving_data) {
      return 'connected';
    }
    if (state === 'starting' || state === 'connecting') {
      return 'connecting';
    }
    if (!health.is_healthy || health.error) {
      return 'error';
    }
    if (state === 'idle' || state === 'stopped') {
      return 'idle';
    }

    return 'unknown';
  }

  // Find matching stream by name (preferred) or sanitized URL (fallback)
  function findMatchingStream(health: StreamHealthResponse): StreamConfig | undefined {
    // Prefer name-based matching for unambiguous identification
    if (health.name) {
      const byName = streams.find(s => s.name === health.name);
      if (byName) return byName;
    }
    // Fallback to URL-based matching (handles legacy responses or missing names)
    return streams.find(
      s => sanitizeUrlForComparison(s.url) === health.url || s.url === health.url
    );
  }

  // Load initial health status
  async function loadHealthStatus() {
    if (healthLoading) return;
    healthLoading = true;

    try {
      const response = await api.get<StreamHealthResponse[]>('/api/v2/streams/health');

      // Clear existing entries first (mutate, don't reassign)
      for (const key of Object.keys(streamHealth)) {
        // eslint-disable-next-line security/detect-object-injection -- Key from Object.keys on internal state object
        delete streamHealth[key];
      }

      // Add new entries by mutation
      if (Array.isArray(response)) {
        response.forEach(health => {
          const matchingStream = findMatchingStream(health);
          if (matchingStream) {
            streamHealth[matchingStream.url] = health;
          }
        });
      }
    } catch (error) {
      logger.warn('Failed to load stream health', error, {
        component: 'StreamManager',
        action: 'loadHealthStatus',
      });
      // Health loading failure is non-critical, stream cards will show "unknown" status
    } finally {
      healthLoading = false;
    }
  }

  // Connect to SSE for real-time updates using ReconnectingEventSource
  function connectSSE() {
    if (eventSource) {
      eventSource.close();
    }

    try {
      // ReconnectingEventSource with configuration for automatic reconnection
      eventSource = new ReconnectingEventSource(buildAppUrl('/api/v2/streams/health/stream'), {
        withCredentials: false,
        max_retry_time: 30000,
      });

      eventSource.addEventListener('stream_health', (event: Event) => {
        try {
          // Cast to access data property from SSE event
          const eventData = (event as unknown as { data: string }).data;
          const data = JSON.parse(eventData) as StreamHealthResponse & {
            event_type: string;
          };

          const matchingStream = findMatchingStream(data);

          if (matchingStream) {
            // Update the state - mutate the object to trigger reactivity
            streamHealth[matchingStream.url] = data;
          }
        } catch (e) {
          logger.warn('Failed to parse stream health event', e, {
            component: 'StreamManager',
          });
        }
      });

      eventSource.onerror = () => {
        logger.warn('Stream health SSE connection error', null, {
          component: 'StreamManager',
        });
        // ReconnectingEventSource handles reconnection automatically
      };
    } catch (error) {
      logger.error('Failed to create ReconnectingEventSource:', error);
    }
  }

  // Handle URL input with auto-detection
  function handleUrlInput(value: string) {
    if (value.includes('://')) {
      newStreamType = detectStreamType(value);
    }
  }

  async function analyzeChannels(url: string) {
    const seq = ++analysisSeq;
    isAnalyzing = true;
    analysisResult = null;
    analysisError = null;
    try {
      const result = await analyzeStreamChannels(url);
      if (seq !== analysisSeq || url.trim() !== newUrl.trim()) return;
      analysisResult = result;
      if (result.recommended && result.recommended !== 'downmix') {
        newChannelMode = result.recommended;
      }
    } catch (err: unknown) {
      if (seq !== analysisSeq) return;
      analysisError = err instanceof Error ? err.message : String(err);
    } finally {
      if (seq === analysisSeq) isAnalyzing = false;
    }
  }

  function handleNewTestResult(result: { sampleRate: number; channels: number } | null) {
    newTestResult = result;
    newTestedUrl = result ? newUrl.trim() : null;
    if (!result) {
      analysisResult = null;
      analysisError = null;
    } else if (result.channels > 1) {
      analyzeChannels(newUrl);
    }
  }

  function resetAddForm() {
    analysisSeq++;
    isAnalyzing = false;
    showAddForm = false;
    newName = '';
    newUrl = '';
    newTransport = 'tcp';
    newStreamType = 'rtsp';
    newChannelMode = 'downmix';
    newGain = 0;
    newModels = defaultModels;
    newQuietHours = { ...defaultQuietHoursConfig };
    newTestResult = null;
    newTestedUrl = null;
    analysisResult = null;
    analysisError = null;
    clearErrors();
  }

  // Clear form errors
  function clearErrors() {
    nameError = null;
    urlError = null;
  }

  // Add new stream
  // Open add form with the default models pre-selected
  function openAddForm() {
    if (disabled) return;
    newModels = defaultModels;
    showAddForm = true;
  }

  // ----- Rows -----

  function newRowIds(count: number): string[] {
    return Array.from({ length: count }, () => generateId(ROW_ID_PREFIX));
  }

  let rowIds = $state<string[]>(untrack(() => newRowIds(streams.length)));
  // One stream opens by default, several start closed.
  const expandedRows = new SvelteSet<string>(untrack(() => (rowIds.length === 1 ? rowIds : [])));

  const rollback = new RowRollback();

  // A stream is identified by its name and URL, the two fields a row's open
  // state and drafts are about; other edits (gain, models) keep the identity.
  function listIdentity(list: readonly StreamConfig[]): string {
    return JSON.stringify(list.map(stream => [stream.name, stream.url]));
  }

  // Identities of the lists this manager has held or sent: the one it started
  // with, each list it sent (add, delete, a card's commit) and the list that
  // was current when it sent, which a refused save takes the list back to. A
  // list outside this history came from elsewhere (a reload, another window)
  // and replaces the history.
  const knownLists: string[] = untrack(() => [listIdentity(streams)]);

  function remember(identity: string) {
    if (knownLists.includes(identity)) return;
    knownLists.push(identity);
    if (knownLists.length > MAX_KNOWN_LISTS) knownLists.shift();
  }

  /** Sends a list to the parent and remembers it, with the list it replaces. */
  function sendStreams(list: StreamConfig[], options: CommitOptions) {
    remember(listIdentity(streams));
    remember(listIdentity(list));
    onUpdateStreams(list, options);
  }

  // A list this manager did not produce gives every row a fresh id, so no open
  // state, status or unapplied draft lands on a different stream, and forgets
  // the rollback baseline. A refused add or delete takes the list back to the
  // rows this manager remembered, which keeps every card's id, open state and
  // drafts; a length change from a list the manager knows but cannot restore
  // also gives fresh ids.
  $effect.pre(() => {
    const count = streams.length;
    const identity = listIdentity(streams);
    untrack(() => {
      const own = knownLists.includes(identity);
      // A foreign list starts a new history that holds only itself: it arriving
      // again keeps the rows, while an older list (a swap and its swap back) is
      // foreign again.
      if (!own) {
        knownLists.length = 0;
        knownLists.push(identity);
      }
      if (own && rowIds.length === count) return;
      const previous = own ? rollback.take(count) : null;
      if (previous) {
        rowIds = previous.ids;
        expandedRows.clear();
        for (const id of previous.expanded) expandedRows.add(id);
        return;
      }
      rollback.clear();
      rowIds = newRowIds(count);
      expandedRows.clear();
      if (count === 1) expandedRows.add(rowIds[0]);
    });
  });

  let rows = $derived(
    streams.map((stream, index) => ({ stream, index, id: rowIds.at(index) ?? `${index}` }))
  );

  function toggleRow(id: string) {
    if (expandedRows.has(id)) expandedRows.delete(id);
    else expandedRows.add(id);
  }

  let addButton = $state<HTMLButtonElement>();

  // ----- Add (saved at once) -----

  let listStatus = $derived(autoSaveStatus(STREAMS_LIST_FIELD));
  let addPending = $state(false);
  let adding = $derived(addPending && listStatus.state === 'saving');
  let addBlocked = $derived(adding || disabled || newNeedsTest);

  // Ends an add: a save closes the form and fetches the new stream's health, a
  // failure keeps the form open with the reason.
  $effect(() => {
    const state = listStatus.state;
    untrack(() => {
      if (state === 'saved') rollback.clear();
      if (!addPending || state === 'saving') return;
      addPending = false;
      if (state === 'error') return;
      resetAddForm();
      clearTimeout(healthRefreshTimer);
      healthRefreshTimer = setTimeout(() => void loadHealthStatus(), HEALTH_REFRESH_AFTER_ADD_MS);
    });
  });

  function addStream() {
    if (addBlocked) return;
    clearErrors();

    const trimmedName = newName.trim();
    const trimmedUrl = newUrl.trim();

    if (!trimmedName) {
      nameError = t('settings.audio.streams.errors.nameRequired');
      return;
    }
    if (isStreamNameTooLong(trimmedName)) {
      nameError = streamNameTooLongMessage();
      return;
    }
    // Check for duplicate name (case-insensitive)
    const nameLower = trimmedName.toLowerCase();
    if (streams.some(s => s.name.toLowerCase() === nameLower)) {
      nameError = t('settings.audio.streams.errors.duplicateName');
      return;
    }
    if (!trimmedUrl) {
      urlError = t('settings.audio.streams.errors.urlRequired');
      return;
    }
    if (!isValidStreamUrl(trimmedUrl, newStreamType)) {
      urlError = t('settings.audio.streams.errors.invalidUrl');
      return;
    }
    if (streams.some(s => s.url === trimmedUrl)) {
      urlError = t('settings.audio.streams.errors.duplicate');
      return;
    }

    // Ensure at least one model is selected when the classifier offers one
    const models = newModels.length > 0 ? newModels : defaultModels;

    // Only RTSP and RTMP take a transport.
    const newStream: StreamConfig = {
      name: trimmedName,
      url: trimmedUrl,
      enabled: true,
      type: newStreamType,
      models,
      channelMode: newChannelMode,
      ...(showTransportInAdd ? { transport: newTransport } : {}),
      gain: newGain,
      quietHours: newQuietHours,
    };

    const rowId = generateId(ROW_ID_PREFIX);
    rollback.remember(rowIds, expandedRows);
    rowIds = [...rowIds, rowId];
    if (streams.length === 0) expandedRows.add(rowId);
    addPending = true;
    sendStreams([...streams, newStream], { field: STREAMS_LIST_FIELD });
  }

  // ----- Change and delete -----

  function commitStream(index: number, updated: StreamConfig, options: CommitOptions) {
    const current = streams.at(index);
    if (!current) return;
    // Health is keyed by URL; a new URL starts without the old one's.
    if (current.url !== updated.url) delete streamHealth[current.url];
    sendStreams(
      streams.map((stream, i) => (i === index ? updated : stream)),
      options
    );
  }

  async function deleteStream(index: number) {
    const streamToDelete = streams.at(index);
    const id = rowIds.at(index);
    rollback.remember(rowIds, expandedRows);
    rowIds = rowIds.filter((_, i) => i !== index);
    if (id !== undefined) expandedRows.delete(id);
    sendStreams(
      streams.filter((_, i) => i !== index),
      { field: STREAMS_LIST_FIELD }
    );
    if (streamToDelete) delete streamHealth[streamToDelete.url];
    // The deleted card took focus with it; land on the add button instead.
    await tick();
    addButton?.focus();
  }

  onMount(() => {
    loadHealthStatus();
    quietHoursStore.startPolling();
    if (streams.length > 0) {
      connectSSE();
    }
  });

  onDestroy(() => {
    clearTimeout(healthRefreshTimer);
    quietHoursStore.stopPolling();
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  });

  // Reconnect SSE when streams change
  $effect(() => {
    if (streams.length > 0 && !eventSource) {
      connectSSE();
    } else if (streams.length === 0 && eventSource) {
      eventSource.close();
      eventSource = null;
    }
  });
</script>

<div class="space-y-4">
  <!-- Status Summary Bar -->
  {#if streams.length > 0}
    <div class="flex items-center justify-between p-3 bg-[var(--color-base-200)] rounded-lg">
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2">
          <Radio class="size-4 text-[var(--color-base-content)]/70" />
          <span class="text-sm font-medium">
            {t('settings.audio.streams.summary', { count: streams.length })}
          </span>
        </div>

        {#if !healthLoading}
          <div class="flex items-center gap-2">
            {#if healthySummary.healthy > 0}
              <StatusPill
                variant="success"
                label="{healthySummary.healthy} {t('settings.audio.streams.healthy')}"
                size="sm"
              />
            {/if}
            {#if healthySummary.unhealthy > 0}
              <StatusPill
                variant="error"
                label="{healthySummary.unhealthy} {t('settings.audio.streams.unhealthy')}"
                size="sm"
              />
            {/if}
            {#if healthySummary.unknown > 0}
              <StatusPill
                variant="neutral"
                label="{healthySummary.unknown} {t('settings.audio.streams.unknown')}"
                size="sm"
              />
            {/if}
            {#if healthySummary.disabled > 0}
              <StatusPill
                variant="neutral"
                label="{healthySummary.disabled} {t('settings.audio.streams.disabled')}"
                size="sm"
              />
            {/if}
          </div>
        {/if}
      </div>

      <button
        type="button"
        class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={loadHealthStatus}
        disabled={healthLoading || disabled}
      >
        <RefreshCw class={cn('size-4', healthLoading && 'animate-spin')} />
        {t('settings.audio.streams.refresh')}
      </button>
    </div>
  {/if}

  <!-- Stream Cards -->
  {#if streams.length === 0 && !showAddForm}
    <EmptyState
      icon={Radio}
      title={t('settings.audio.streams.emptyState.title')}
      description={t('settings.audio.streams.emptyState.description')}
      hints={[
        t('settings.audio.streams.emptyState.hints.rtsp'),
        t('settings.audio.streams.emptyState.hints.credentials'),
        t('settings.audio.streams.emptyState.hints.protocol'),
      ]}
      hintsTitle={t('settings.audio.streams.emptyState.hintsTitle')}
      primaryAction={{
        label: t('settings.audio.streams.addStream'),
        icon: Plus,
        onclick: openAddForm,
      }}
    />
  {:else}
    <div class="space-y-3">
      {#each rows as row (row.id)}
        <StreamCard
          stream={row.stream}
          index={row.index}
          {streams}
          rowId={row.id}
          status={getStreamStatus(row.stream.url, row.stream)}
          {availableModels}
          {modelOptions}
          {disabled}
          expanded={expandedRows.has(row.id)}
          onToggleExpanded={() => toggleRow(row.id)}
          onCommit={(updated, options) => commitStream(row.index, updated, options)}
          onDelete={() => void deleteStream(row.index)}
        />
      {/each}
    </div>

    {#if !showAddForm}
      <AutoSaveStatus field={STREAMS_LIST_FIELD} />
    {/if}

    <!-- Add Stream Section -->
    {#if showAddForm}
      <div
        class="rounded-lg overflow-hidden bg-[var(--color-base-200)] border border-[var(--color-primary)]"
      >
        <div class="p-6">
          <h3 class="text-base font-semibold">
            {t('settings.audio.streams.addStream')}
          </h3>

          <div class="space-y-4 mt-4">
            <!-- Stream Name -->
            <div>
              <TextInput
                id="new-stream-name"
                bind:value={newName}
                label={t('settings.audio.streams.nameLabel')}
                placeholder={t('settings.audio.streams.namePlaceholder')}
                helpText={nameError ? undefined : t('settings.audio.streams.nameHelp')}
                maxlength={MAX_STREAM_NAME_LENGTH}
                required
                {disabled}
                error={nameError ?? undefined}
                oninput={() => (nameError = null)}
              />
            </div>

            <!-- Stream URL -->
            <div>
              <TextInput
                id="new-stream-url"
                bind:value={newUrl}
                label={t('settings.audio.streams.urlLabel')}
                placeholder="rtsp://user:password@192.168.1.100:554/stream"
                helpText={urlError ? undefined : t('settings.audio.streams.urlHelp')}
                required
                oninput={handleUrlInput}
                {disabled}
              />
              {#if urlError}
                <p
                  role="alert"
                  aria-live="assertive"
                  class="text-xs text-[var(--text-error)] -mt-2"
                >
                  {urlError}
                </p>
              {/if}
            </div>

            <!-- Test Stream -->
            <StreamTestButton
              url={newUrl}
              models={availableModels}
              selectedModels={newModels}
              {disabled}
              onResult={handleNewTestResult}
            />

            <!-- Stream Type and Protocol -->
            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <SelectDropdown
                value={newStreamType}
                label={t('settings.audio.streams.typeLabel')}
                options={streamTypeOptions}
                {disabled}
                onChange={value => (newStreamType = toStreamType(value) ?? newStreamType)}
                groupBy={false}
                menuSize="sm"
              />

              {#if showTransportInAdd}
                <SelectDropdown
                  value={newTransport}
                  label={t('settings.audio.streams.transportLabel')}
                  options={transportOptions}
                  {disabled}
                  onChange={value => {
                    if (value === 'tcp' || value === 'udp') newTransport = value;
                  }}
                  groupBy={false}
                  menuSize="sm"
                />
              {/if}
            </div>

            <!-- Channel handling: format display, selector, and stereo analysis -->
            <StreamChannelControls
              channelMode={newChannelMode}
              channels={newDetectedChannels}
              sampleRate={newDetectedSampleRate}
              analyzeUrl={newUrl}
              {isAnalyzing}
              {analysisResult}
              {analysisError}
              {disabled}
              onChange={mode => (newChannelMode = mode)}
              onAnalyze={url => analyzeChannels(url)}
            />

            <!-- Gain -->
            <InlineSlider
              label={t('settings.audio.soundCards.gainLabel')}
              value={newGain}
              onUpdate={value => (newGain = value)}
              min={AUDIO_GAIN_MIN_DB}
              max={AUDIO_GAIN_MAX_DB}
              step={1}
              unit=" dB"
              {disabled}
            />

            <!-- Model Selection -->
            <div class="space-y-2">
              <h4 id="new-stream-models" class="text-sm font-semibold">
                {t('settings.audio.soundCards.modelLabel')}
              </h4>
              <ModelToggleList
                models={availableModels}
                selectedModels={newModels}
                {defaultModels}
                sourceName={newName.trim()}
                sourceSampleRate={newSourceSampleRate}
                isStream
                loading={modelsLoading()}
                failed={modelsFailed()}
                availability={acousticAvailability}
                labelledBy="new-stream-models"
                {disabled}
                onChange={models => (newModels = models)}
                onRetry={() => void refreshModels()}
              />
            </div>

            <!-- Quiet Hours -->
            <QuietHoursEditor
              config={newQuietHours}
              onChange={qh => (newQuietHours = qh)}
              {disabled}
              idPrefix="new-stream-qh"
            />

            <!-- Action Buttons -->
            <div class="flex flex-wrap items-center justify-end gap-2 pt-2">
              <div class="mr-auto flex min-w-0 flex-col gap-1">
                <div id={ADD_SAVING_STATUS_ID}>
                  <AutoSaveStatus field={STREAMS_LIST_FIELD} />
                </div>
                {#if newNeedsTest}
                  <p id="new-stream-test-required" class="text-xs text-muted">
                    {t('settings.audio.streams.testRequired')}
                  </p>
                {/if}
              </div>
              <button
                type="button"
                class="btn btn-ghost btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                aria-disabled={adding ? 'true' : undefined}
                aria-describedby={adding ? ADD_SAVING_STATUS_ID : undefined}
                onclick={() => {
                  if (!adding) resetAddForm();
                }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm gap-1.5 aria-disabled:cursor-not-allowed aria-disabled:opacity-60"
                aria-disabled={addBlocked ? 'true' : undefined}
                aria-describedby={newNeedsTest ? 'new-stream-test-required' : undefined}
                onclick={addStream}
              >
                <Plus class="size-4" aria-hidden="true" />
                {adding
                  ? t('settings.audio.soundCards.adding')
                  : t('settings.audio.streams.addStream')}
              </button>
            </div>
          </div>
        </div>
      </div>
    {:else}
      <!-- Add Stream Button -->
      <button
        bind:this={addButton}
        type="button"
        class="w-full inline-flex items-center justify-center gap-2 h-8 px-3 text-sm rounded-lg border border-dashed border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-content)]/5 text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={openAddForm}
        {disabled}
      >
        <Plus class="size-4" />
        {t('settings.audio.streams.addStream')}
      </button>
    {/if}
  {/if}
</div>

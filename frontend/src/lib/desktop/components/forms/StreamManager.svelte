<!--
  Stream Manager Component

  Purpose: Manage the audio streams of a tab that saves with the page: stream
  cards with live health, an add form that appends the new stream to the form,
  delete, a status summary bar and empty state guidance.

  Every change goes out through onUpdateStreams as the whole streams array; the
  page's Save Changes bar persists it. Rows carry a UI-only id (never persisted)
  that keys the cards and their expanded state. The manager remembers the
  identity (name and URL of every row) of the list it last sent; a list with
  another identity (Reset, a reload) is foreign and gives every row a fresh id.
  Health is shown for the saved streams only.

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
  import { SvelteMap, SvelteSet } from 'svelte/reactivity';
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
  import { modelLabel } from '$lib/utils/modelLabel';
  import { streamProblems } from './sourceRules';
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
    streamHasTransport,
    streamNameProblemMessage,
    streamUrlProblemMessage,
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
    /** The saved streams: health follows them and only problems the user introduced are reported. */
    savedStreams: StreamConfig[];
    /** URLs tested in this page session, kept by the page so they outlive a tab switch. */
    testedUrls: ReadonlySet<string>;
    disabled?: boolean;
    /** Why the cards and the add form are disabled, shown beside their blocked actions. */
    disabledReason?: string;
    onUpdateStreams: (_streams: StreamConfig[]) => void;
    /**
     * Called with the trimmed URL of every successful connection test, from a card or
     * from the add form.
     */
    onUrlTested: (_url: string) => void;
  }

  let {
    streams = [],
    savedStreams,
    testedUrls,
    disabled = false,
    disabledReason,
    onUpdateStreams,
    onUrlTested,
  }: Props = $props();

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
  // A URL needs a test unless this form tested it or the page knows it passed one this session.
  let newNeedsTest = $derived(
    newUrl.trim() !== '' && newTestedUrl !== newUrl.trim() && !testedUrls.has(newUrl.trim())
  );
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

    streams.forEach((stream, index) => {
      if (!stream.enabled) {
        disabledCount++;
        return;
      }

      const healthUrl = healthUrlOf(rowIds.at(index), stream);
      // eslint-disable-next-line security/detect-object-injection -- saved URL from validated stream config, not user input
      const health = healthUrl === undefined ? undefined : streamHealth[healthUrl];
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
  function isStreamSuppressed(url: string, savedStream: StreamConfig): boolean {
    const qhStatus = quietHoursStore.status;
    if (!savedStream.quietHours?.enabled || !qhStatus?.suppressedStreams) return false;
    const sanitized = sanitizeUrlForComparison(url);
    // eslint-disable-next-line security/detect-object-injection -- sanitized URL from internal sanitizeUrlForComparison, not user input
    return qhStatus.suppressedStreams[sanitized] === true;
  }

  // Convert backend process state to UI status
  function getStreamStatus(healthUrl: string | undefined, stream: StreamConfig): StreamStatus {
    if (!stream.enabled) {
      return 'disabled';
    }

    // eslint-disable-next-line security/detect-object-injection -- URL from validated stream config, not user input
    const health = healthUrl === undefined ? undefined : streamHealth[healthUrl];
    if (!health) {
      // No health data means the stream was removed from the active map.
      // Check per-stream suppression state rather than global anyActive flag.
      // Suppression belongs to the saved stream: its saved quiet hours decide, not the
      // draft, and a card without a saved counterpart must not borrow another's status.
      const saved =
        healthUrl === undefined ? undefined : savedStreams.find(s => s.url === healthUrl);
      if (healthUrl !== undefined && saved && isStreamSuppressed(healthUrl, saved)) {
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
      const byName = savedStreams.find(s => s.name === health.name);
      if (byName) return byName;
    }
    // Fallback to URL-based matching (handles legacy responses or missing names)
    return savedStreams.find(
      s => sanitizeUrlForComparison(s.url) === health.url || s.url === health.url
    );
  }

  // Load initial health status
  async function loadHealthStatus() {
    if (healthLoading) return;
    healthLoading = true;
    // The saved names and URLs this request belongs to: the entries are matched against
    // the saved list, so a response for an older list must not be applied to a newer one.
    const requestedIdentity = listIdentity(savedStreams);

    try {
      const response = await api.get<StreamHealthResponse[]>('/api/v2/streams/health');
      if (requestedIdentity !== listIdentity(savedStreams)) return;

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
      // The saved list changed while the request ran: ask again for the latest one.
      if (requestedIdentity !== listIdentity(savedStreams)) void loadHealthStatus();
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
  // The trimmed URL of the field at its last input.
  let lastNewUrl = '';
  // The channel choice came from an analysis, not from the user: it belongs to the URL
  // that was analyzed and goes back to the default when the URL changes.
  let channelModeIsAutomatic = false;

  function handleUrlInput(value: string) {
    urlError = null;
    const trimmed = value.trim();
    if (trimmed.includes('://')) {
      newStreamType = detectStreamType(trimmed);
    }
    if (trimmed === lastNewUrl) return;
    lastNewUrl = trimmed;
    // A run, a result or a rejection of an earlier URL must not land on this one.
    analysisSeq++;
    isAnalyzing = false;
    analysisResult = null;
    analysisError = null;
    if (channelModeIsAutomatic) {
      newChannelMode = 'downmix';
      channelModeIsAutomatic = false;
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
        channelModeIsAutomatic = true;
      }
    } catch (err: unknown) {
      if (seq !== analysisSeq) return;
      analysisError = err instanceof Error ? err.message : String(err);
    } finally {
      if (seq === analysisSeq) isAnalyzing = false;
    }
  }

  function handleNewTestResult(
    result: { sampleRate: number; channels: number } | null,
    testedUrl: string
  ) {
    // A result for an earlier URL (a probe that finished after the field changed)
    // says nothing about the URL the form holds now.
    if (testedUrl.trim() !== newUrl.trim()) return;
    newTestResult = result;
    newTestedUrl = result ? testedUrl : null;
    if (result) onUrlTested(testedUrl);
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
    channelModeIsAutomatic = false;
    lastNewUrl = '';
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
    if (addEntryBlocked) return;
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

  // The saved stream (name and URL) each row came from, by row id. Health belongs to
  // the saved stream, so a row shows it only while its URL is still that saved URL: an
  // edited URL, or a URL copied from another stream, shows none. A row counts as saved
  // when its name and URL are those of a saved stream at the moment its id is given out
  // (the first list, Reset, a reload) or the saved list is replaced (a Save). The map is
  // rebuilt from scratch each time: a row keeps its earlier saved source (through an
  // unsaved rename, say) only while that same saved source still exists, never just
  // because another saved stream now uses the URL.
  interface SavedSource {
    name: string;
    url: string;
  }
  const savedRowSources = new SvelteMap<string, SavedSource>();

  function sourceKey(source: SavedSource): string {
    return JSON.stringify([source.name, source.url]);
  }

  function adoptSavedUrls() {
    const savedKeys = new Set(savedStreams.map(sourceKey));
    const next = new Map<string, SavedSource>();
    streams.forEach((stream, index) => {
      const id = rowIds.at(index);
      if (id === undefined) return;
      const known = savedRowSources.get(id);
      if (savedKeys.has(sourceKey(stream))) next.set(id, { name: stream.name, url: stream.url });
      else if (known !== undefined && savedKeys.has(sourceKey(known))) next.set(id, known);
    });
    savedRowSources.clear();
    next.forEach((source, id) => savedRowSources.set(id, source));
  }

  /** The URL a row's health is filed under: its saved URL while the row still has it. */
  function healthUrlOf(id: string | undefined, stream: StreamConfig): string | undefined {
    const saved = id === undefined ? undefined : savedRowSources.get(id)?.url;
    return saved !== undefined && saved === stream.url ? saved : undefined;
  }

  untrack(adoptSavedUrls);

  // A stream is identified by its name and URL, the fields a card's open state
  // is about; other edits keep the identity.
  function listIdentity(list: readonly StreamConfig[]): string {
    return JSON.stringify(list.map(stream => [stream.name, stream.url]));
  }

  // The identity of the list this manager last held or sent.
  let expectedIdentity = untrack(() => listIdentity(streams));

  /** Sends a list to the parent and remembers its identity as the expected one. */
  function sendStreams(list: StreamConfig[]) {
    expectedIdentity = listIdentity(list);
    onUpdateStreams(list);
  }

  // A list with another name and URL sequence than the one this manager sent
  // (Reset, a reload) gives every row a fresh id, so the cards are recreated and
  // no open confirmation or open state lands on a different stream.
  $effect.pre(() => {
    const identity = listIdentity(streams);
    untrack(() => {
      if (identity === expectedIdentity) return;
      expectedIdentity = identity;
      rowIds = newRowIds(streams.length);
      expandedRows.clear();
      savedRowSources.clear();
      adoptSavedUrls();
      if (streams.length === 1) expandedRows.add(rowIds[0]);
    });
  });

  let problems = $derived(streamProblems(streams, savedStreams, testedUrls));
  let rows = $derived(
    streams.map((stream, index) => ({ stream, index, id: rowIds.at(index) ?? `${index}` }))
  );

  function toggleRow(id: string) {
    if (expandedRows.has(id)) expandedRows.delete(id);
    else expandedRows.add(id);
  }

  let addButton = $state<HTMLButtonElement>();

  // ----- Add -----

  let addBlocked = $derived(disabled || newNeedsTest);
  /** Id of the reason line shown below the manager while it is disabled, and of the add form's test reason. */
  const blockedReasonId = generateId('streams-blocked-reason');
  const addTestRequiredId = generateId('add-stream-test-required');
  // The reason line says why the manager is disabled, or that the health load runs.
  let blockedReason = $derived(
    disabled ? (disabledReason ?? t('common.loading')) : t('common.loading')
  );
  // Add stream (both entry points) and Refresh wait for a running health load too.
  let addEntryBlocked = $derived(disabled || healthLoading);
  let refreshDescribedBy = $derived(addEntryBlocked ? blockedReasonId : undefined);
  let addDescribedBy = $derived(
    [disabled ? blockedReasonId : undefined, newNeedsTest ? addTestRequiredId : undefined]
      .filter(Boolean)
      .join(' ') || undefined
  );

  function buildNewStream(): StreamConfig {
    // Only RTSP and RTMP take a transport.
    return {
      name: newName.trim(),
      url: newUrl.trim(),
      enabled: true,
      type: newStreamType,
      models: newModels.length > 0 ? newModels : defaultModels,
      channelMode: newChannelMode,
      ...(showTransportInAdd ? { transport: newTransport } : {}),
      gain: newGain,
      quietHours: newQuietHours,
    };
  }

  /**
   * What is wrong with the new stream against the current rows only: the saved list is
   * not a reason to accept it, and the test requirement is checked apart (addBlocked).
   */
  function newStreamProblem(newStream: StreamConfig) {
    return streamProblems([...streams, newStream], [], new Set([newStream.url])).at(-1);
  }

  // An empty or malformed name or URL shows when its field is left, ahead of the
  // reminder that the URL needs a test.
  function validateNameOnBlur() {
    const problem = newStreamProblem(buildNewStream());
    nameError = problem?.name ? streamNameProblemMessage(problem.name) : null;
  }

  function validateUrlOnBlur() {
    const problem = newStreamProblem(buildNewStream());
    urlError = problem?.url ? streamUrlProblemMessage(problem.url) : null;
  }

  function addStream() {
    if (addBlocked) return;
    clearErrors();

    const newStream = buildNewStream();
    const problem = newStreamProblem(newStream);
    if (problem?.name) {
      nameError = streamNameProblemMessage(problem.name);
      return;
    }
    if (problem?.url) {
      urlError = streamUrlProblemMessage(problem.url);
      return;
    }

    const rowId = generateId(ROW_ID_PREFIX);
    rowIds = [...rowIds, rowId];
    if (streams.length === 0) expandedRows.add(rowId);
    sendStreams([...streams, newStream]);
    resetAddForm();
  }

  // ----- Change and delete -----

  // A card writes by the row id it was rendered with. A write that arrives after
  // its row went (a late analysis result of a deleted card, or of one a Reset
  // replaced) finds no row and is dropped instead of landing on the stream that
  // took its index.
  function changeStream(id: string, updated: StreamConfig) {
    const index = rowIds.indexOf(id);
    if (index < 0 || !streams.at(index)) return;
    sendStreams(streams.map((stream, i) => (i === index ? updated : stream)));
  }

  async function deleteStream(index: number) {
    const id = rowIds.at(index);
    rowIds = rowIds.filter((_, i) => i !== index);
    if (id !== undefined) {
      expandedRows.delete(id);
      savedRowSources.delete(id);
    }
    sendStreams(streams.filter((_, i) => i !== index));
    // The deleted card took focus with it; land on the add button instead.
    await tick();
    addButton?.focus();
  }

  onMount(() => {
    loadHealthStatus();
    quietHoursStore.startPolling();
  });

  onDestroy(() => {
    quietHoursStore.stopPolling();
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  });

  // Follow the saved streams: a stream exists on the server only after Save, so
  // the SSE connects while any is saved and the health is fetched again when the
  // saved list changes.
  let savedIdentity: string | undefined;
  $effect(() => {
    const identity = listIdentity(savedStreams);
    const hasSaved = savedStreams.length > 0;
    untrack(() => {
      if (hasSaved && !eventSource) connectSSE();
      else if (!hasSaved && eventSource) {
        eventSource.close();
        eventSource = null;
      }
      if (savedIdentity !== undefined && savedIdentity !== identity) void loadHealthStatus();
      savedIdentity = identity;
    });
  });

  // The saved list is replaced by every Save (also by one that leaves its names and
  // URLs as they were), so the row to saved URL map is rebuilt on each replacement.
  $effect(() => {
    void savedStreams;
    untrack(adoptSavedUrls);
  });
</script>

<!-- The label of a TextInput, drawn here so the input need not carry the native required
     attribute (its validation message is fixed English and would show beside ours). -->
{#snippet requiredLabel(inputId: string, text: string)}
  <label class="label justify-start" for={inputId}>
    <span class="label-text capitalize">
      {text}
      <span class="ml-1 text-[var(--text-error)]" aria-hidden="true">*</span>
    </span>
  </label>
{/snippet}

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

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="inline-flex items-center justify-center gap-1.5 h-8 px-3 text-sm rounded-lg bg-transparent hover:bg-[var(--color-base-content)]/10 text-[var(--color-base-content)] transition-colors aria-disabled:opacity-50 aria-disabled:cursor-not-allowed"
          onclick={() => {
            if (!disabled) void loadHealthStatus();
          }}
          aria-disabled={healthLoading || disabled ? 'true' : undefined}
          aria-describedby={refreshDescribedBy}
        >
          <RefreshCw class={cn('size-4', healthLoading && 'animate-spin')} />
          {t('settings.audio.streams.refresh')}
        </button>
      </div>
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
        disabled: addEntryBlocked,
        describedBy: blockedReasonId,
      }}
    />
  {:else}
    <div class="space-y-3">
      {#each rows as row (row.id)}
        <StreamCard
          stream={row.stream}
          status={getStreamStatus(healthUrlOf(row.id, row.stream), row.stream)}
          healthUrl={healthUrlOf(row.id, row.stream)}
          {availableModels}
          {modelOptions}
          {disabled}
          {disabledReason}
          expanded={expandedRows.has(row.id)}
          onToggleExpanded={() => toggleRow(row.id)}
          problem={problems.at(row.index)}
          {onUrlTested}
          onChange={updated => changeStream(row.id, updated)}
          onDelete={() => void deleteStream(row.index)}
        />
      {/each}
    </div>

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
              {@render requiredLabel('new-stream-name', t('settings.audio.streams.nameLabel'))}
              <TextInput
                id="new-stream-name"
                bind:value={newName}
                placeholder={t('settings.audio.streams.namePlaceholder')}
                helpText={nameError ? undefined : t('settings.audio.streams.nameHelp')}
                maxlength={MAX_STREAM_NAME_LENGTH}
                aria-required="true"
                {disabled}
                error={nameError ?? undefined}
                oninput={() => (nameError = null)}
                onblur={validateNameOnBlur}
              />
            </div>

            <!-- Stream URL -->
            <div>
              {@render requiredLabel('new-stream-url', t('settings.audio.streams.urlLabel'))}
              <TextInput
                id="new-stream-url"
                bind:value={newUrl}
                placeholder="rtsp://user:password@192.168.1.100:554/stream"
                helpText={urlError || newNeedsTest
                  ? undefined
                  : t('settings.audio.streams.urlHelp')}
                aria-required="true"
                oninput={handleUrlInput}
                onblur={validateUrlOnBlur}
                {disabled}
                error={urlError ??
                  (newNeedsTest ? t('settings.audio.streams.testRequired') : undefined)}
              />
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
                onChange={value => {
                  newStreamType = toStreamType(value) ?? newStreamType;
                  urlError = null;
                }}
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
              onChange={mode => {
                newChannelMode = mode;
                channelModeIsAutomatic = false;
              }}
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
                {#if newNeedsTest}
                  <p id={addTestRequiredId} class="text-xs text-muted">
                    {t('settings.audio.streams.testRequired')}
                  </p>
                {/if}
              </div>
              <button
                type="button"
                class="btn btn-ghost btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                onclick={resetAddForm}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm gap-1.5 aria-disabled:cursor-not-allowed aria-disabled:opacity-60"
                aria-disabled={addBlocked ? 'true' : undefined}
                aria-describedby={addDescribedBy}
                onclick={addStream}
              >
                <Plus class="size-4" aria-hidden="true" />
                {t('settings.audio.streams.addStream')}
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
        class="w-full inline-flex items-center justify-center gap-2 h-8 px-3 text-sm rounded-lg border border-dashed border-[var(--border-200)] bg-transparent hover:bg-[var(--color-base-content)]/5 text-[var(--color-base-content)] transition-colors aria-disabled:opacity-50 aria-disabled:cursor-not-allowed"
        onclick={openAddForm}
        aria-disabled={addEntryBlocked ? 'true' : undefined}
        aria-describedby={addEntryBlocked ? blockedReasonId : undefined}
      >
        <Plus class="size-4" />
        {t('settings.audio.streams.addStream')}
      </button>
    {/if}
  {/if}

  {#if addEntryBlocked}
    <p id={blockedReasonId} class="text-sm text-[var(--text-subtle)]">{blockedReason}</p>
  {/if}
</div>

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import SystemInference from './SystemInference.svelte';
import type {
  InferenceStatusResponse,
  InferenceModel,
  InferenceHardware,
} from '$lib/desktop/features/system/inference.types';

// The component talks to the JSON API and opens an SSE stream. The API is mocked
// per test so each state can be driven deterministically, and the SSE source is a
// no-op stub so jsdom never tries to open a real EventSource.

// Hold a reference to the mocked api.get so each test can install its own handler.
const apiGet = vi.fn();

vi.mock('$lib/utils/api', () => ({
  api: {
    get: (url: string) => apiGet(url),
  },
  // Minimal ApiError stand-in; the view only references the class indirectly.
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status = 0) {
      super(message);
      this.status = status;
    }
  },
}));

// SSE stub: the component constructs one, registers listeners, and closes it on
// unmount. None of those should touch the network in tests. The stub captures
// the registered listeners so tests can fire SSE events (e.g. a topology change)
// without opening a real EventSource.
const sseListeners = new Map<string, (event: Event) => void>();

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    addEventListener(type: string, listener: (event: Event) => void): void {
      sseListeners.set(type, listener);
    }
    close(): void {}
  },
}));

/** Fire a captured SSE listener by event type, if one was registered. */
function fireSseEvent(type: string, data?: unknown): void {
  const listener = sseListeners.get(type);
  if (!listener) return;
  const event = data === undefined ? new Event(type) : { data: JSON.stringify(data) };
  listener(event as Event);
}

const inferenceTest = createComponentTestFactory(SystemInference);

// The snapshot endpoint and the metrics-history endpoint share api.get, so the
// handler branches on the URL.
const INFERENCE_URL = '/api/v2/system/inference';
const HISTORY_FRAGMENT = '/metrics/history';

/** Build a minimal valid model, overridable per test. */
function makeModel(overrides: Partial<InferenceModel> = {}): InferenceModel {
  return {
    id: 'model-1',
    name: 'BirdNET GLOBAL 6K',
    backend: 'ONNX',
    quantization: 'INT8',
    isStock: true,
    spec: { sampleRate: 48000, clipLengthSec: 3 },
    numSpecies: 6522,
    stats: { invocations: 12034, avgMs: 47.2, maxMs: 130, rtf: 0.016 },
    memory: { approxRssBytes: 125000000, approximate: true },
    sources: [{ id: 'mic1', name: 'Front Yard', type: 'soundcard', fallback: false }],
    metricKeys: {
      avgMs: 'inference.model-1.avg_ms',
      rtf: 'inference.model-1.rtf',
      throughput: 'inference.model-1.throughput',
      errorRate: 'inference.model-1.error_rate',
    },
    device: 'CPU',
    paused: false,
    recentDetections: [],
    ...overrides,
  };
}

/** Build a minimal valid snapshot, overridable per test. */
function makeSnapshot(
  models: InferenceModel[],
  hardware: Partial<InferenceHardware> = {}
): InferenceStatusResponse {
  return {
    hardware: {
      arch: 'amd64',
      cpuModel: 'Test CPU',
      // Capitalised to match sysinfo.GetEnvironment's EnvDocker verbatim; the
      // container/host icon choice is a case-sensitive prefix match on it.
      environment: 'Docker',
      fp16: true,
      ...hardware,
    },
    backends: {
      tflite: { available: true },
      onnx: { available: true, initialized: true, version: '1.18' },
      openvino: { supported: false, active: false },
    },
    models,
    audio: {
      queueDepth: 0,
      droppedChunksTotal: 0,
      queueCapacity: 64,
      metricKeys: { queueDepth: 'audio.queue_depth' },
    },
    snapshotAtUnix: 1750000000,
  };
}

/**
 * Install an api.get handler that returns the given snapshot for the inference URL
 * and the given metrics-history payload for the history URL.
 */
function installApi(
  snapshot: InferenceStatusResponse,
  history: { metrics: Record<string, { timestamp: string; value: number }[]> } = { metrics: {} }
): void {
  apiGet.mockImplementation((url: string) => {
    if (url.includes(HISTORY_FRAGMENT)) {
      return Promise.resolve(history);
    }
    if (url.includes(INFERENCE_URL)) {
      return Promise.resolve(snapshot);
    }
    return Promise.resolve({ metrics: {} });
  });
}

describe('SystemInference', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset captured SSE listeners so each test starts from a clean slate.
    sseListeners.clear();
  });

  afterEach(() => {
    cleanup();
  });

  it('shows a loading indicator while the snapshot request is pending', async () => {
    // Never-resolving snapshot request keeps the view in its loading state.
    apiGet.mockImplementation(() => new Promise(() => {}));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      const status = container.querySelector('[role="status"]');
      expect(status).not.toBeNull();
    });
    // The mocked i18n returns the key when no translation is registered.
    expect(container.textContent).toContain('system.inference.loading');
  });

  it('renders the empty state when the snapshot has no models', async () => {
    installApi(makeSnapshot([]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.noModels');
    });
    // No model cards: the empty-state message stands alone.
    expect(container.textContent).not.toContain('system.inference.invocations');
  });

  it('renders models sorted by name regardless of snapshot order', async () => {
    // Snapshot delivers Zebra before Alpha; the view must display Alpha first.
    const zebra = makeModel({ id: 'z', name: 'Zebra Model' });
    const alpha = makeModel({ id: 'a', name: 'Alpha Model' });
    installApi(makeSnapshot([zebra, alpha]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('Alpha Model');
    });
    const text = container.textContent;
    expect(text.indexOf('Alpha Model')).toBeLessThan(text.indexOf('Zebra Model'));
  });

  it('does not render an RTF sparkline label on model cards', async () => {
    installApi(makeSnapshot([makeModel()]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.invocations');
    });
    // The RTF sparkline (labelled rtfChart) is removed; the RTF number stat stays.
    expect(container.textContent).not.toContain('system.inference.rtfChart');
    // Assert the RTF number cell specifically (its span carries the rtfHelp title),
    // not a loose substring of textContent that rtfHelp would also satisfy.
    expect(container.querySelector('[title="system.inference.rtfHelp"]')).not.toBeNull();
  });

  it('does not render its own page title heading', async () => {
    installApi(makeSnapshot([makeModel()]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.invocations');
    });
    // Sibling system subpages render no title; System.svelte provides the region label.
    expect(container.querySelector('h2')).toBeNull();
  });

  it('renders the audio-sources hint link in the empty state', async () => {
    installApi(makeSnapshot([]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.noModels');
    });
    const link = container.querySelector('a[href="/ui/settings/audio"]');
    expect(link).not.toBeNull();
    expect(link?.textContent).toContain('system.inference.noModelsHintLink');
  });

  it('exposes screen-reader help for jargon terms', async () => {
    installApi(makeSnapshot([makeModel()]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.invocations');
    });
    // The stat help snippet renders sr-only spans carrying the *Help keys.
    expect(container.textContent).toContain('system.inference.invocationsHelp');
    expect(container.textContent).toContain('system.inference.rtfHelp');
  });

  it('renders model details, backend, quantization, invocations and a source chip', async () => {
    const model = makeModel();
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });

    const text = container.textContent;
    // Backend and quantization render verbatim via Badge text.
    expect(text).toContain('ONNX');
    expect(text).toContain('INT8');
    // Invocations render formatted (formatNumber inserts a thousands separator).
    expect(text).toContain('12,034');
    // Source chip shows the source name.
    expect(text).toContain('Front Yard');
  });

  // The "not analyzing" source chip. The i18n stub returns the key for unmapped
  // strings, so assertions target the key names. The Badge component renders a
  // <span>; its error+outline variant is identified by the CSS custom property
  // classes, and its help text is an sr-only span referenced by aria-describedby.
  describe('not-analyzing source chip', () => {
    /** The Badge outer spans that carry the not-running help association. */
    function notRunningBadges(container: HTMLElement): HTMLSpanElement[] {
      return Array.from(
        container.querySelectorAll<HTMLSpanElement>('span[aria-describedby^="source-not-running-"]')
      );
    }

    it('renders the not-analyzing label and error styling when notRunning is true', async () => {
      const model = makeModel({
        sources: [
          { id: 'mic1', name: 'Front Yard', type: 'soundcard', fallback: false, notRunning: true },
        ],
      });
      installApi(makeSnapshot([model]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Front Yard');
      });

      expect(container.textContent).toContain('system.inference.sourceNotRunning');

      const badges = notRunningBadges(container);
      expect(badges).toHaveLength(1);
      const badge = badges[0];
      // error + outline variant classes from Badge.svelte.
      expect(badge.className).toContain('text-[var(--color-error)]');
      expect(badge.className).toContain('border-[var(--color-error)]');
      expect(badge.getAttribute('title')).toBe('system.inference.sourceNotRunningTooltip');
    });

    it('omits the not-analyzing label and error styling when notRunning is ABSENT (omitempty contract)', async () => {
      // notRunning is omitted entirely, mirroring Go's json:"notRunning,omitempty".
      const model = makeModel({
        sources: [{ id: 'mic1', name: 'Front Yard', type: 'soundcard', fallback: false }],
      });
      installApi(makeSnapshot([model]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Front Yard');
      });

      // No label and no tooltip anywhere (the label key is also a prefix of the
      // tooltip key, so a single absence assertion covers both).
      expect(container.textContent).not.toContain('system.inference.sourceNotRunning');
      expect(notRunningBadges(container)).toHaveLength(0);
    });

    it('behaves like the absent case when notRunning is false', async () => {
      const model = makeModel({
        sources: [
          { id: 'mic1', name: 'Front Yard', type: 'soundcard', fallback: false, notRunning: false },
        ],
      });
      installApi(makeSnapshot([model]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Front Yard');
      });

      expect(container.textContent).not.toContain('system.inference.sourceNotRunning');
      expect(notRunningBadges(container)).toHaveLength(0);
    });

    it('associates aria-describedby with a real element that carries the tooltip text', async () => {
      const model = makeModel({
        sources: [
          { id: 'mic1', name: 'Front Yard', type: 'soundcard', fallback: false, notRunning: true },
        ],
      });
      installApi(makeSnapshot([model]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Front Yard');
      });

      const badge = notRunningBadges(container)[0];
      const helpId = badge.getAttribute('aria-describedby');
      expect(helpId).toBeTruthy();
      const help = container.querySelector(`[id="${helpId}"]`);
      expect(help).not.toBeNull();
      expect(help?.textContent).toContain('system.inference.sourceNotRunningTooltip');
    });

    it('renders the label once and generates unique help ids when only one of several sources is not running', async () => {
      const model = makeModel({
        sources: [
          { id: 'a', name: 'Front Yard', type: 'soundcard', fallback: false },
          { id: 'b', name: 'Back Yard', type: 'rtsp', fallback: false, notRunning: true },
          { id: 'c', name: 'Garage', type: 'soundcard', fallback: false },
        ],
      });
      installApi(makeSnapshot([model]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Back Yard');
      });

      // Exactly one source is flagged, so exactly one badge carries the association
      // and one help element exists.
      const badges = notRunningBadges(container);
      expect(badges).toHaveLength(1);

      const helpSpans = Array.from(
        container.querySelectorAll<HTMLElement>('span[id^="source-not-running-"]')
      );
      const ids = helpSpans.map(s => s.id);
      expect(ids).toHaveLength(1);
      // Guards the duplicate-id family behind issue #4190: every generated id is unique.
      expect(new Set(ids).size).toBe(ids.length);

      // Every aria-describedby resolves to an element that actually exists.
      for (const badge of badges) {
        const helpId = badge.getAttribute('aria-describedby');
        expect(container.querySelector(`[id="${helpId}"]`)).not.toBeNull();
      }
    });
  });

  // These tests run against the i18n stub in src/test/setup.ts, which returns the
  // key for any unmapped string, so assertions target the rendered key names
  // (system.inference.vad.*) plus the data values, not the English text.
  it('hides the VAD panel when the snapshot carries no vad block', async () => {
    installApi(makeSnapshot([makeModel()]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('BirdNET GLOBAL 6K');
    });
    expect(container.textContent).not.toContain('system.inference.vad.section');
  });

  it('renders the VAD panel with active state, stats and recent-speech feed', async () => {
    const snapshot = makeSnapshot([makeModel()]);
    snapshot.vad = {
      enabled: true,
      available: true,
      loaded: true,
      threshold: 0.35,
      modelSource: 'embedded',
      strategy: 'sequence',
      sampleRate: 16000,
      stats: { invocations: 90211, avgMs: 2.4, maxMs: 9.1, speechHits: 9042 },
      lastSpeechAtUnix: 1750000000,
      lastSpeechProbability: 0.82,
      recentHits: [
        { atUnix: 1750000000, probability: 0.87, source: 'Front Yard' },
        { atUnix: 1749999500, probability: 0.91, source: 'Back Yard' },
      ],
    };
    installApi(snapshot);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.vad.section');
    });
    // Scope every VAD assertion to the VAD card. Page-wide text also contains the
    // co-rendered model card and the hardware fixture, which carry their own CPU,
    // sample-rate, invocation and latency labels; asserting against the whole
    // container would let this test keep passing after the VAD markup regresses.
    const vadCard = container.querySelector('[data-testid="vad-card"]');
    expect(vadCard).not.toBeNull();
    const vadText = vadCard?.textContent ?? '';
    expect(vadText).toContain('system.inference.vad.title'); // card name (like a model name)
    expect(vadText).toContain('system.inference.vad.active'); // active indicator
    expect(vadText).toContain('CPU'); // device badge
    expect(vadText).toContain('system.inference.sampleRate'); // spec line sample rate
    expect(vadText).toContain('16'); // sampleRateKhz(16000) => "16"
    expect(vadText).toContain('0.35'); // threshold value
    expect(vadText).toContain('system.inference.invocations'); // stats line segments analysed
    expect(vadText).toContain('system.inference.avgLatency'); // stats line avg latency
    expect(vadText).toContain('system.inference.maxLatency'); // stats line max latency
    expect(vadText).toContain('90,211'); // invocations value
    expect(vadText).toContain('9,042'); // speech hits value
    // Recent-speech history feed: the source names and probabilities render.
    expect(vadText).toContain('system.inference.vad.recentTitle');
    expect(vadText).toContain('Front Yard');
    expect(vadText).toContain('Back Yard');
    expect(vadText).toContain('87%'); // hit probability rounded to a percentage
  });

  it('renders multiple VAD hits with identical timestamps and sources without crashing', async () => {
    const snapshot = makeSnapshot([makeModel()]);
    snapshot.vad = {
      enabled: true,
      available: true,
      loaded: true,
      threshold: 0.35,
      stats: { invocations: 10, avgMs: 2.0, maxMs: 5.0, speechHits: 2 },
      recentHits: [
        { atUnix: 1750000000, probability: 0.85, source: 'SoundCard' },
        { atUnix: 1750000000, probability: 0.88, source: 'SoundCard' },
      ],
    };
    installApi(snapshot);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.vad.section');
    });
    const vadCard = container.querySelector('[data-testid="vad-card"]');
    expect(vadCard).not.toBeNull();
    expect(vadCard?.textContent).toContain('SoundCard');
  });

  it('shows the empty recent-speech state when there are no hits', async () => {
    const snapshot = makeSnapshot([makeModel()]);
    snapshot.vad = {
      enabled: true,
      available: true,
      loaded: true,
      threshold: 0.35,
      stats: { invocations: 42, avgMs: 2.4, maxMs: 9.1, speechHits: 0 },
      recentHits: [],
    };
    installApi(snapshot);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.vad.section');
    });
    expect(container.textContent).toContain('system.inference.vad.recentEmpty');
  });

  it('shows the disabled VAD state without stale descriptors', async () => {
    const snapshot = makeSnapshot([makeModel()]);
    snapshot.vad = {
      enabled: false,
      available: true,
      loaded: false,
      threshold: 0.35,
      stats: { invocations: 0, avgMs: 0, maxMs: 0, speechHits: 0 },
    };
    installApi(snapshot);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.vad.section');
    });
    const text = container.textContent;
    expect(text).toContain('system.inference.vad.disabled');
    expect(text).not.toContain('system.inference.vad.active'); // not the active state
  });

  it('shows the not-measured label when approxRssBytes is absent', async () => {
    // Drop the memory measurement: ramDisplay falls back to the not-measured label.
    const model = makeModel({ memory: { approximate: true } });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.textContent).toContain('system.inference.notMeasured');
  });

  it('shows a dash for RTF when the model has zero invocations', async () => {
    // Zero invocations with a NON-null rtf: only the invocations<=0 branch can
    // produce the dash here (the rtf==null branch is excluded), so this test
    // exercises the invocation guard rather than the missing-value guard.
    const model = makeModel({
      stats: { invocations: 0, avgMs: 0, maxMs: 0, rtf: 0.5 },
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });

    // The RTF cell sits in a span carrying the rtfHelp title; assert its text is "-".
    const rtfCell = container.querySelector('[title="system.inference.rtfHelp"]');
    expect(rtfCell).not.toBeNull();
    expect(rtfCell?.textContent).toContain('-');
    // The non-null rtf value must NOT leak through despite being present.
    expect(rtfCell?.textContent).not.toContain('0.5');
  });

  it('ignores history series for models not in the snapshot and still renders the valid model', async () => {
    const model = makeModel();
    // History includes the valid model's keys plus an orphan key for a model that
    // is no longer in the snapshot. The orphan carries a distinctive ghost value
    // (99999) so the test can prove the orphan key was actually rejected rather
    // than just not crashing. Two points per valid series so the Sparkline
    // renders a path (a single point produces no path), proving ingestion.
    const GHOST_VALUE = 99999;
    const history = {
      metrics: {
        'inference.model-1.avg_ms': [
          { timestamp: '2026-06-18T00:00:00Z', value: 47.2 },
          { timestamp: '2026-06-18T00:00:01Z', value: 48.1 },
        ],
        'inference.model-1.rtf': [
          { timestamp: '2026-06-18T00:00:00Z', value: 0.016 },
          { timestamp: '2026-06-18T00:00:01Z', value: 0.018 },
        ],
        'inference.ghost-model.avg_ms': [{ timestamp: '2026-06-18T00:00:00Z', value: GHOST_VALUE }],
        'inference.ghost-model.rtf': [{ timestamp: '2026-06-18T00:00:00Z', value: 1.5 }],
      },
    };
    installApi(makeSnapshot([model]), history);

    const { container } = inferenceTest.render({});

    // The valid model renders without throwing despite the orphan series.
    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });

    // The orphan/ghost series value must not surface anywhere in the rendered
    // output: the orphan key was rejected by the validKeys gate.
    expect(container.textContent).not.toContain(String(GHOST_VALUE));

    // The valid model's series was ingested: with two points its Sparkline
    // renders at least one line path (a single point would render none).
    expect(container.querySelector('svg path')).not.toBeNull();
  });

  it('re-fetches the snapshot when a topology-change SSE event fires', async () => {
    // Topology event name must match the backend constant and the component.
    const TOPOLOGY_EVENT = 'system.inference_topology_changed';

    // Start with a single model. A non-empty history seed makes the component
    // connect the SSE stream (and register the topology listener), rather than
    // falling back to polling.
    const firstModel = makeModel({ id: 'model-1', name: 'BirdNET GLOBAL 6K' });
    const firstHistory = {
      metrics: {
        'inference.model-1.avg_ms': [{ timestamp: '2026-06-18T00:00:00Z', value: 47.2 }],
        'inference.model-1.rtf': [{ timestamp: '2026-06-18T00:00:00Z', value: 0.016 }],
      },
    };
    installApi(makeSnapshot([firstModel]), firstHistory);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(firstModel.name);
    });

    // The stream must have registered a topology listener (and opened no real
    // EventSource: the stub is a no-op constructor with no network access).
    await waitFor(() => {
      expect(sseListeners.has(TOPOLOGY_EVENT)).toBe(true);
    });

    const snapshotCallsBefore = apiGet.mock.calls.filter(
      (call: unknown[]) => typeof call[0] === 'string' && call[0].includes(INFERENCE_URL)
    ).length;

    // Swap the API to a DIFFERENT snapshot (a second, distinctly named model),
    // then fire the captured topology listener.
    const secondModel = makeModel({
      id: 'model-2',
      name: 'PERCH SECONDARY 9K',
      metricKeys: {
        avgMs: 'inference.model-2.avg_ms',
        rtf: 'inference.model-2.rtf',
        throughput: 'inference.model-2.throughput',
        errorRate: 'inference.model-2.error_rate',
      },
    });
    installApi(makeSnapshot([secondModel]));

    fireSseEvent(TOPOLOGY_EVENT);

    // The component re-fetched the snapshot endpoint and rendered the new model.
    await waitFor(() => {
      expect(container.textContent).toContain(secondModel.name);
    });
    const snapshotCallsAfter = apiGet.mock.calls.filter(
      (call: unknown[]) => typeof call[0] === 'string' && call[0].includes(INFERENCE_URL)
    ).length;
    expect(snapshotCallsAfter).toBeGreaterThan(snapshotCallsBefore);
  });

  it('does not render the Audio card (intentionally hidden in Phase A)', async () => {
    const snap = makeSnapshot([makeModel()]);
    snap.audio = {
      queueDepth: 7,
      droppedChunksTotal: 42,
      queueCapacity: 64,
      metricKeys: { queueDepth: 'audio.queue_depth' },
    };
    installApi(snap);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.invocations');
    });
    // The Audio pipeline card is deliberately hidden pending a refactor; its
    // section header must not appear.
    expect(container.textContent).not.toContain('system.inference.sectionAudio');
  });

  it('does not include the audio queue-depth key in the metrics subscription', async () => {
    // With the Audio card hidden in Phase A, metricKeysParam() must subscribe only
    // to per-model series and must NOT include the audio queue-depth key.
    const model = makeModel();
    let capturedHistoryUrl = '';
    apiGet.mockImplementation((url: string) => {
      if (url.includes('/metrics/history')) {
        capturedHistoryUrl = url;
        return Promise.resolve({ metrics: {} });
      }
      if (url.includes(INFERENCE_URL)) {
        return Promise.resolve(makeSnapshot([model]));
      }
      return Promise.resolve({ metrics: {} });
    });

    inferenceTest.render({});

    await waitFor(() => {
      expect(capturedHistoryUrl).toContain('inference.model-1.avg_ms');
    });
    expect(capturedHistoryUrl).not.toContain('audio.queue_depth');
    // Throughput must stay subscribed: the activity pulse derives from it even
    // though its sparkline was removed. RTF and error-rate are rendered from the
    // snapshot, not a live series, so they are intentionally NOT subscribed.
    expect(capturedHistoryUrl).toContain('inference.model-1.throughput');
    expect(capturedHistoryUrl).not.toContain('inference.model-1.rtf');
    expect(capturedHistoryUrl).not.toContain('inference.model-1.error_rate');
  });

  it('renders the Last heard feed with recent detections (species and confidence)', async () => {
    const model = makeModel({
      recentDetections: [
        {
          species: 'Common Chaffinch',
          scientificName: 'Fringilla coelebs',
          confidence: 0.87,
          atUnix: Math.floor(Date.now() / 1000) - 60,
          inRange: true,
        },
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.42,
          atUnix: Math.floor(Date.now() / 1000) - 120,
          inRange: true,
        },
      ],
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    const text = container.textContent;
    expect(text).toContain('system.inference.lastHeard');
    expect(text).toContain('Common Chaffinch');
    expect(text).toContain('87%');
    expect(text).toContain('European Robin');
    expect(text).toContain('42%');
  });

  it('shows the same species more than once in the throttled feed', async () => {
    // The feed is chronological, not collapsed: a species detected again after
    // its throttle interval appears as a separate row (no ×N counter, no range).
    const now = Math.floor(Date.now() / 1000);
    const model = makeModel({
      recentDetections: [
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.81,
          atUnix: now - 2,
          inRange: true,
        },
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.77,
          atUnix: now - 12,
          inRange: true,
        },
      ],
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    // Two separate rows for the same species, each with its own confidence.
    expect(container.querySelectorAll('tbody tr')).toHaveLength(2);
    const text = container.textContent;
    expect(text).toContain('81%');
    expect(text).toContain('77%');
    expect(text).not.toContain('×');
  });

  it('renders multiple detections with identical species and timestamps without crashing', async () => {
    const now = 1750000000;
    const model = makeModel({
      recentDetections: [
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.81,
          atUnix: now,
          inRange: true,
        },
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.77,
          atUnix: now,
          inRange: true,
        },
      ],
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.querySelectorAll('tbody tr')).toHaveLength(2);
  });

  it('lists other models that detected the same species within tolerance (Also column)', async () => {
    const now = Math.floor(Date.now() / 1000);
    const birdnet = makeModel({
      id: 'model-1',
      name: 'BirdNET GLOBAL 6K',
      detectionName: 'BirdNET',
      recentDetections: [
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.8,
          atUnix: now - 5,
          inRange: true,
        },
        {
          species: 'Common Blackbird',
          scientificName: 'Turdus merula',
          confidence: 0.7,
          atUnix: now - 30,
          inRange: true,
        },
      ],
    });
    const perch = makeModel({
      id: 'model-2',
      name: 'Perch',
      detectionName: 'Perch',
      metricKeys: {
        avgMs: 'inference.model-2.avg_ms',
        rtf: 'inference.model-2.rtf',
        throughput: 'inference.model-2.throughput',
        errorRate: 'inference.model-2.error_rate',
      },
      // Robin 1s from BirdNET's Robin (within tolerance); no Blackbird.
      recentDetections: [
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.75,
          atUnix: now - 6,
          inRange: true,
        },
      ],
    });
    installApi(makeSnapshot([birdnet, perch]));

    const { container } = inferenceTest.render({});
    await waitFor(() => {
      expect(container.textContent).toContain('BirdNET GLOBAL 6K');
    });

    // The "Also" column is the 4th cell of each detection row.
    const alsoCells = Array.from(container.querySelectorAll('tbody tr td:nth-child(4)')).map(c =>
      c.textContent.trim()
    );
    expect(alsoCells).toContain('Perch'); // BirdNET's Robin co-detected by Perch
    expect(alsoCells).toContain('BirdNET'); // Perch's Robin co-detected by BirdNET
    expect(alsoCells).toContain('-'); // Blackbird had no co-detection
  });

  it('marks out-of-range / non-avian predictions with an icon, not in-range ones', async () => {
    const now = Math.floor(Date.now() / 1000);
    const model = makeModel({
      recentDetections: [
        {
          species: 'Engine',
          scientificName: 'Engine',
          confidence: 0.7,
          atUnix: now - 2,
          inRange: false,
        },
        {
          species: 'European Robin',
          scientificName: 'Erithacus rubecula',
          confidence: 0.8,
          atUnix: now - 10,
          inRange: true,
        },
      ],
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});
    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });

    // Exactly one out-of-range marker (Engine); the in-range Robin has none.
    const markers = container.querySelectorAll('[aria-label="system.inference.outOfRangeHelp"]');
    expect(markers).toHaveLength(1);
    expect(container.textContent).toContain('Engine');
    expect(container.textContent).toContain('European Robin');
  });

  it('shows the lastHeardNever label when there are no recent detections', async () => {
    const model = makeModel({ recentDetections: [] });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.textContent).toContain('system.inference.lastHeardNever');
  });

  it('shows activity pulse as active when throughput series last value is > 0', async () => {
    const model = makeModel();
    const history = {
      metrics: {
        [model.metricKeys.avgMs]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 47.2 },
          { timestamp: '2026-06-18T00:00:01Z', value: 48.1 },
        ],
        [model.metricKeys.rtf]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 0.016 },
          { timestamp: '2026-06-18T00:00:01Z', value: 0.018 },
        ],
        [model.metricKeys.throughput]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 0 },
          { timestamp: '2026-06-18T00:00:01Z', value: 3.5 },
        ],
      },
    };
    installApi(makeSnapshot([model]), history);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    // When the last throughput value > 0, the activity indicator shows "active" with label
    const activeEl = container.querySelector('[aria-label="system.inference.activityActive"]');
    expect(activeEl).not.toBeNull();
    expect(activeEl?.textContent).toContain('system.inference.active');
    // The idle indicator must NOT be rendered at the same time (regression guard)
    const idleEl = container.querySelector('[aria-label="system.inference.activityIdle"]');
    expect(idleEl).toBeNull();
  });

  it('shows activity pulse as idle when throughput series last value is 0', async () => {
    const model = makeModel();
    const history = {
      metrics: {
        [model.metricKeys.avgMs]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 47.2 },
          { timestamp: '2026-06-18T00:00:01Z', value: 48.1 },
        ],
        [model.metricKeys.rtf]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 0.016 },
          { timestamp: '2026-06-18T00:00:01Z', value: 0.018 },
        ],
        [model.metricKeys.throughput]: [
          { timestamp: '2026-06-18T00:00:00Z', value: 3.5 },
          { timestamp: '2026-06-18T00:00:01Z', value: 0 },
        ],
      },
    };
    installApi(makeSnapshot([model]), history);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    // When the last throughput value == 0, the activity indicator shows "idle" with label
    const idleEl = container.querySelector('[aria-label="system.inference.activityIdle"]');
    expect(idleEl).not.toBeNull();
    expect(idleEl?.textContent).toContain('system.inference.activityIdle');
    // The active indicator must NOT be rendered at the same time (regression guard)
    const activeEl = container.querySelector('[aria-label="system.inference.activityActive"]');
    expect(activeEl).toBeNull();
  });

  it('renders errorRate and loadFailures when present on the model', async () => {
    const model = makeModel({
      stats: {
        invocations: 100,
        avgMs: 45,
        maxMs: 120,
        rtf: 0.015,
        errorRate: 0.05,
        loadFailures: 3,
      },
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    const text = container.textContent;
    expect(text).toContain('system.inference.errorRate');
    expect(text).toContain('5%');
    expect(text).toContain('system.inference.loadFailures');
    expect(text).toContain('3');
  });

  it('renders the device chip carrying the model device', async () => {
    const model = makeModel({ device: 'GPU' });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.textContent).toContain('GPU');
    // The device chip carries the device help as a tooltip.
    expect(container.querySelector('[title="system.inference.deviceHelp"]')).not.toBeNull();
  });

  it('shows a Paused indicator with the schedule label when the model is paused', async () => {
    const model = makeModel({ paused: true, scheduleLabel: 'Night schedule' });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    // The paused state replaces the idle dash and explains why inference is stopped.
    expect(
      container.querySelector('[aria-label="system.inference.activityPaused"]')
    ).not.toBeNull();
    expect(container.textContent).toContain('system.inference.paused');
    expect(container.textContent).toContain('Night schedule');
    // Neither active nor idle indicators render while paused.
    expect(container.querySelector('[aria-label="system.inference.activityActive"]')).toBeNull();
    expect(container.querySelector('[aria-label="system.inference.activityIdle"]')).toBeNull();
  });

  describe('compute precision (Inference Backends footer)', () => {
    // The contract this suite pins, in one place: the mocked t() returns the key
    // itself, so these are the strings that actually reach the DOM.
    const LABEL_KEY = 'system.inference.fp16';
    const SUPPORTED_KEY = 'system.inference.fp16Supported';
    const UNSUPPORTED_KEY = 'system.inference.fp16Unsupported';
    const BACKENDS_HEADING_KEY = 'system.inference.sectionBackends';
    const CARD_SELECTOR = 'div.rounded-xl';

    // The label key must be matched EXACTLY, never as a substring of
    // textContent: LABEL_KEY is a prefix of SUPPORTED_KEY, so a contains-check
    // passes even when the label is missing entirely and asserts nothing.
    function precisionLabel(container: HTMLElement): Element | undefined {
      return [...container.querySelectorAll('span')].find(
        el => el.textContent.trim() === LABEL_KEY
      );
    }

    // The row lives in the Inference Backends card rather than the Hardware
    // card, because native FP16 is a property of the CPU every backend executes
    // on. Both branches are asserted: the label renders either way, so only the
    // pill distinguishes a working readout from one stuck on one variant.
    it('reports FP16 as supported when the CPU has native half precision', async () => {
      installApi(makeSnapshot([makeModel({})], { fp16: true }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(SUPPORTED_KEY);
      });
      expect(precisionLabel(container)).toBeDefined();
      expect(container.textContent).not.toContain(UNSUPPORTED_KEY);
    });

    // Placement, not just presence. Every other assertion here passes equally
    // against the pre-move code, because the row kept its keys and its id when
    // it changed cards; only these two fail if it moves back.
    it('places the precision row in the Backends card, not the Hardware card', async () => {
      installApi(makeSnapshot([makeModel({})], { fp16: true }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(SUPPORTED_KEY);
      });
      const card = precisionLabel(container)?.closest(CARD_SELECTOR);
      expect(card?.textContent).toContain(BACKENDS_HEADING_KEY);
      // The Hardware card is the definition list; the row must have left it.
      expect(container.querySelector('dl')?.textContent).not.toContain(SUPPORTED_KEY);
    });

    it('reports FP16 as unsupported when the CPU lacks native half precision', async () => {
      installApi(makeSnapshot([makeModel({})], { fp16: false }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(UNSUPPORTED_KEY);
      });
      expect(precisionLabel(container)).toBeDefined();
      expect(container.textContent).not.toContain(SUPPORTED_KEY);
    });

    // The label carries aria-describedby pointing at an sr-only span, so the
    // explanation reaches a screen reader and not only a hovering mouse. The id
    // and the reference come from one constant; this proves they still resolve.
    it('links the precision label to its screen-reader description', async () => {
      installApi(makeSnapshot([makeModel({})], { fp16: true }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(SUPPORTED_KEY);
      });
      const label = precisionLabel(container);
      const describedBy = label?.getAttribute('aria-describedby');
      expect(describedBy).toBeTruthy();
      expect(container.querySelector(`#${describedBy}`)).not.toBeNull();
    });
  });

  describe('detected hardware panel', () => {
    it('renders board, cores and memory when the probe found them', async () => {
      installApi(
        makeSnapshot([makeModel({})], {
          board: {
            kind: 'raspberry-pi',
            model: 'Raspberry Pi 5 Model B Rev 1.0',
            soc: 'bcm2712',
            tier: 'pi5',
          },
          physicalCores: 4,
          totalRamBytes: 4 * 1024 * 1024 * 1024,
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Raspberry Pi 5 Model B Rev 1.0');
      });
      expect(container.textContent).toContain('bcm2712');
      expect(container.textContent).toContain('system.inference.cores');
      expect(container.textContent).toContain('system.inference.memory');
      expect(container.textContent).toContain('4.0 GB');
    });

    it('still shows the board when the device tree gave only an SoC', async () => {
      // The server sends a board when it resolved a model OR an SoC. Gating the
      // row on the model would discard the one fact the probe recovered.
      installApi(
        makeSnapshot([makeModel({})], {
          board: { kind: 'generic', soc: 'rk3588' },
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('rk3588');
      });
      expect(container.textContent).toContain('system.inference.board');
    });

    it('omits the board row on a host with no device tree', async () => {
      installApi(makeSnapshot([makeModel({})]));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('system.inference.sectionHardware');
      });
      expect(container.textContent).not.toContain('system.inference.board');
      expect(container.textContent).not.toContain('system.inference.memory');
    });

    it('renders two identical GPUs without crashing', async () => {
      // Two cards of the same model produce byte-identical names, because the
      // name carries no PCI slot. A keyed {#each} on the name would throw
      // each_key_duplicate here and blank the entire page, in production too.
      const card = {
        kind: 'dgpu',
        vendor: 'nvidia',
        name: 'NVIDIA Graphics [10de:2504]',
        accessible: true,
        reasons: ['no-runtime' as const],
      };
      installApi(makeSnapshot([makeModel({})], { accelerators: [card, { ...card }] }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.querySelectorAll('[role="group"]')).toHaveLength(2);
      });
      expect(container.textContent).toContain('system.inference.gpuReasonNoRuntime');
    });

    it('lists every blocker for a GPU the server cannot reach', async () => {
      installApi(
        makeSnapshot([makeModel({})], {
          accelerators: [
            {
              kind: 'dgpu',
              vendor: 'amd',
              name: 'AMD Graphics [1002:73ff]',
              accessible: false,
              reasons: ['no-runtime', 'render-node-unavailable'],
            },
          ],
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('AMD Graphics [1002:73ff]');
      });
      expect(container.textContent).toContain('system.inference.gpuNotReachable');
      expect(container.textContent).toContain('system.inference.gpuReasonNoRuntime');
      expect(container.textContent).toContain('system.inference.gpuReasonRenderNodeUnavailable');
    });

    it('shows a reachable GPU as reachable and still lists a vendor blocker', async () => {
      // Reachable and unusable are independent: an AMD card can be perfectly
      // reachable while no build ships a runtime for it.
      installApi(
        makeSnapshot([makeModel({})], {
          accelerators: [
            {
              kind: 'igpu',
              vendor: 'intel',
              name: 'Intel Graphics [8086:9a49]',
              accessible: true,
            },
          ],
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('Intel Graphics [8086:9a49]');
      });
      expect(container.textContent).toContain('system.inference.gpuReachable');
      expect(container.textContent).not.toContain('system.inference.gpuReason');
    });

    it('falls back to a generic label for an unrecognised reason code', async () => {
      // A newer server can emit a reason this bundle has no translation for;
      // the panel must still say the GPU is unusable rather than render blank.
      installApi(
        makeSnapshot([makeModel({})], {
          accelerators: [
            {
              kind: 'dgpu',
              vendor: 'amd',
              name: 'AMD Graphics [1002:73ff]',
              accessible: false,
              reasons: ['brand-new-code' as unknown as 'no-runtime'],
            },
          ],
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain('AMD Graphics [1002:73ff]');
      });
      expect(container.textContent).toContain('system.inference.gpuReasonUnknown');
    });
  });

  describe('hardware capability tokens (Advanced disclosure)', () => {
    // The mocked t() returns the key, so these are the strings that reach the DOM.
    const ADVANCED_KEY = 'system.inference.advanced';
    const CAPABILITIES_KEY = 'system.inference.capabilities';
    const HARDWARE_HEADING_KEY = 'system.inference.sectionHardware';

    it('hides the disclosure when the host reports no capability tokens', async () => {
      installApi(makeSnapshot([makeModel({})], {})); // fixture omits capabilities

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(HARDWARE_HEADING_KEY);
      });
      expect(container.textContent).not.toContain(ADVANCED_KEY);
    });

    it('reveals the tokens as badges when the host reports them', async () => {
      installApi(
        makeSnapshot([makeModel({})], {
          capabilities: ['low-ram', 'openvino-gpu-intel-gen12'],
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(ADVANCED_KEY);
      });
      // The disclosure must be collapsed by default (token soup stays hidden).
      expect(container.querySelector('details[open]')).toBeNull();
      expect(container.textContent).toContain(CAPABILITIES_KEY);
      // The two non-derivable tokens this feature exists to surface.
      expect(container.textContent).toContain('low-ram');
      expect(container.textContent).toContain('openvino-gpu-intel-gen12');
    });

    // Placement, not just presence: the tokens describe the host, so the
    // disclosure must live in the Hardware card (the definition-list card), not
    // the Backends card. This fails if it is moved next to the FP16 footer.
    it('places the disclosure in the Hardware card', async () => {
      installApi(makeSnapshot([makeModel({})], { capabilities: ['low-ram'] }));

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(ADVANCED_KEY);
      });
      const card = container.querySelector('details')?.closest('div.rounded-xl');
      expect(card?.textContent).toContain(HARDWARE_HEADING_KEY);
      expect(card?.querySelector('dl')).not.toBeNull();
    });

    it('renders duplicate capability tokens without crashing', async () => {
      installApi(
        makeSnapshot([makeModel({})], {
          capabilities: ['low-ram', 'low-ram'],
        })
      );

      const { container } = inferenceTest.render({});

      await waitFor(() => {
        expect(container.textContent).toContain(ADVANCED_KEY);
      });
      expect(container.textContent).toContain('low-ram');
    });
  });

  it('renders model sources with duplicate identifiers without crashing', async () => {
    const model = makeModel({
      sources: [
        { id: 'mic', name: 'Microphone', type: 'soundcard', fallback: false },
        { id: 'mic', name: 'Microphone', type: 'soundcard', fallback: false },
      ],
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.textContent).toContain('Microphone');
  });
});

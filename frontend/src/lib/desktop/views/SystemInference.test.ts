import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import SystemInference from './SystemInference.svelte';
import type {
  InferenceStatusResponse,
  InferenceModel,
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
    ...overrides,
  };
}

/** Build a minimal valid snapshot, overridable per test. */
function makeSnapshot(models: InferenceModel[]): InferenceStatusResponse {
  return {
    hardware: { arch: 'amd64', cpuModel: 'Test CPU', environment: 'docker', fp16: true },
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

    // The RTF cell sits in a span carrying the rtfLabel title; assert its text is "-".
    const rtfCell = container.querySelector('[title="system.inference.rtfLabel"]');
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

  it('renders the audio card with queue depth, capacity, and dropped chunk count', async () => {
    const snap = makeSnapshot([]);
    snap.audio = {
      queueDepth: 7,
      droppedChunksTotal: 42,
      queueCapacity: 64,
      metricKeys: { queueDepth: 'audio.queue_depth' },
    };
    installApi(snap);

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain('system.inference.sectionAudio');
    });
    const text = container.textContent;
    expect(text).toContain('7');
    expect(text).toContain('64');
    expect(text).toContain('42');
  });

  it('renders species name and confidence when lastDetection is present', async () => {
    const model = makeModel({
      lastDetection: {
        species: 'Common Chaffinch',
        scientificName: 'Fringilla coelebs',
        confidence: 0.87,
        atUnix: Math.floor(Date.now() / 1000) - 60,
      },
    });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    const text = container.textContent;
    expect(text).toContain('Common Chaffinch');
    expect(text).toContain('87%');
  });

  it('shows the lastSeenNever label when lastDetection is absent', async () => {
    const model = makeModel({ lastDetection: undefined });
    installApi(makeSnapshot([model]));

    const { container } = inferenceTest.render({});

    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
    expect(container.textContent).toContain('system.inference.lastSeenNever');
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
    // When the last throughput value > 0, the activity indicator shows "active"
    const activeEl = container.querySelector('[aria-label="system.inference.activityActive"]');
    expect(activeEl).not.toBeNull();
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
    // When the last throughput value == 0, the activity indicator shows "idle"
    const idleEl = container.querySelector('[aria-label="system.inference.activityIdle"]');
    expect(idleEl).not.toBeNull();
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
});

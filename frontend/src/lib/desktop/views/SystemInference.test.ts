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

// No-op SSE stub: the component constructs one, registers listeners, and closes
// it on unmount. None of those should touch the network in tests.
vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    addEventListener(_type: string, _listener: (event: Event) => void): void {}
    close(): void {}
  },
}));

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
    metricKeys: { avgMs: 'inference.model-1.avg_ms', rtf: 'inference.model-1.rtf' },
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
    // Zero invocations: rtf is meaningless, so rtfDisplay returns "-".
    const model = makeModel({
      stats: { invocations: 0, avgMs: 0, maxMs: 0 },
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
  });

  it('ignores history series for models not in the snapshot and still renders the valid model', async () => {
    const model = makeModel();
    // History includes the valid model's keys plus an orphan key for a model that
    // is no longer in the snapshot. The orphan must be ignored, not crash the view.
    const history = {
      metrics: {
        'inference.model-1.avg_ms': [{ timestamp: '2026-06-18T00:00:00Z', value: 47.2 }],
        'inference.model-1.rtf': [{ timestamp: '2026-06-18T00:00:00Z', value: 0.016 }],
        'inference.ghost-model.avg_ms': [{ timestamp: '2026-06-18T00:00:00Z', value: 99 }],
        'inference.ghost-model.rtf': [{ timestamp: '2026-06-18T00:00:00Z', value: 1.5 }],
      },
    };
    installApi(makeSnapshot([model]), history);

    const { container } = inferenceTest.render({});

    // The valid model renders without throwing despite the orphan series.
    await waitFor(() => {
      expect(container.textContent).toContain(model.name);
    });
  });
});

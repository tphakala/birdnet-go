import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import DetectionCardMobile from './DetectionCardMobile.svelte';
import type { Detection } from '$lib/types/detection.types';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import { appState } from '$lib/stores/appState.svelte';
import { DEFAULT_SPECTROGRAM_SETTINGS, settingsStore } from '$lib/stores/settings';
import { seedSavedRealtime } from '../../../../../test/settings-store-helpers';
import { installFakeMediaElement, settle } from '../../../../../test/fakeMediaElement';
import { mockAudioProbe } from '../../../../../test/audioProbeMock';

// The play overlay builds its Web Audio graph on the first play; the graph is not
// under test here.
vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn(),
  disconnectAudioNodes: vi.fn(),
}));

function createDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 655,
    date: '2024-01-15',
    time: '10:30:00',
    beginTime: '2024-01-15T10:30:00Z',
    endTime: '2024-01-15T10:30:03Z',
    speciesCode: 'amerob',
    commonName: 'American Robin',
    scientificName: 'Turdus migratorius',
    confidence: 0.85,
    verified: 'unverified',
    locked: false,
    clipName: 'clip_655.wav',
    ...overrides,
  };
}

describe('DetectionCardMobile playback denoise default', () => {
  const PRESET = 'light-medium';
  let ctl: ReturnType<typeof installFakeMediaElement>;

  beforeEach(() => {
    vi.useFakeTimers();
    ctl = installFakeMediaElement();
    appState.playbackDenoise = PRESET;
  });

  afterEach(() => {
    appState.playbackDenoise = '';
    ctl.restore();
    vi.useRealTimers();
  });

  it('plays the denoised clip on the first play and loads the stored one before', async () => {
    render(DetectionCardMobile, { props: { detection: createDetection() } });
    expect(new URL(ctl.element.src).search).toBe('');

    ctl.completeLoad();
    await fireEvent.click(screen.getAllByRole('button', { name: /play/i })[0]);
    await settle();

    expect(new URL(ctl.element.src).search).toBe(`?denoise=${PRESET}`);
  });

  describe('audible bats request', () => {
    const SERVER_ERROR_STATUS = 500;
    let restoreProbe: (() => void) | undefined;
    let probe: typeof globalThis.fetch;
    let batsBodies: Array<{ denoise: string }>;

    beforeEach(() => {
      batsBodies = [];
      restoreProbe = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      probe = globalThis.fetch;
      globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (!String(input).includes('/audible-bats')) return probe(input, init);
        batsBodies.push(JSON.parse(String(init?.body)));
        return new Response('wav-data', { status: 200 });
      });
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:bats');
    });

    afterEach(() => {
      globalThis.fetch = probe;
      restoreProbe?.();
      vi.restoreAllMocks();
    });

    async function playBatDetection() {
      render(DetectionCardMobile, {
        props: { detection: createDetection({ modelType: 'bat' }) },
      });
      ctl.completeLoad();
      await fireEvent.click(screen.getAllByRole('button', { name: /play/i })[0]);
      await settle();
    }

    async function enableBats() {
      await fireEvent.click(screen.getByLabelText('media.audio.audibleBats.title'));
      await fireEvent.click(screen.getByText('media.audio.audibleBats.enable'));
      await settle();
    }

    it('sends the playback default', async () => {
      await playBatDetection();
      await enableBats();

      expect(batsBodies.map(body => body.denoise)).toEqual([PRESET]);
    });

    it('sends no denoise once the denoised clip failed', async () => {
      await playBatDetection();
      ctl.failLoad();
      await settle();
      await enableBats();

      expect(batsBodies.map(body => body.denoise)).toEqual(['']);
    });
  });
});

describe('DetectionCardMobile playback gain and high-pass defaults', () => {
  const SETTINGS_BUTTON = 'media.audio.settings';
  const RESET_BUTTON = 'common.reset';
  const DEFAULT_GAIN_DB = -6.5;
  const DEFAULT_CUTOFF_HZ = 300;
  const MOVED_CUTOFF_HZ = 1000;

  beforeEach(() => {
    appState.playbackDefaults = { gainDb: DEFAULT_GAIN_DB, highPassHz: DEFAULT_CUTOFF_HZ };
  });

  afterEach(() => {
    appState.playbackDefaults = { gainDb: 0, highPassHz: 0 };
  });

  function sliders(container: HTMLElement) {
    const [gain, filter] = Array.from(
      container.querySelectorAll<HTMLInputElement>('input[type="range"]')
    );
    return { gain, filter };
  }

  it('starts on the defaults without the modified dot', async () => {
    const { container } = render(DetectionCardMobile, { props: { detection: createDetection() } });
    const trigger = screen.getByRole('button', { name: SETTINGS_BUTTON });
    expect(container.querySelector('.settings-indicator')).toBeNull();

    await fireEvent.click(trigger);

    expect(sliders(container).gain.value).toBe(String(DEFAULT_GAIN_DB));
    expect(sliders(container).filter.value).toBe(String(DEFAULT_CUTOFF_HZ));
  });

  it('shows the dot once the filter moves and Reset returns to the defaults', async () => {
    const { container } = render(DetectionCardMobile, { props: { detection: createDetection() } });
    await fireEvent.click(screen.getByRole('button', { name: SETTINGS_BUTTON }));

    await fireEvent.input(sliders(container).filter, {
      target: { value: String(MOVED_CUTOFF_HZ) },
    });
    expect(container.querySelector('.settings-indicator')).not.toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: RESET_BUTTON }));

    expect(sliders(container).filter.value).toBe(String(DEFAULT_CUTOFF_HZ));
    expect(sliders(container).gain.value).toBe(String(DEFAULT_GAIN_DB));
    expect(container.querySelector('.settings-indicator')).toBeNull();
  });
});

describe('DetectionCardMobile dynamic spectrogram defaults', () => {
  const OriginalIO = globalThis.IntersectionObserver;
  const initialStore = get(settingsStore);
  const initialAppSpec = { ...appState.spectrogramDefaults };
  let statusUrls: string[];

  beforeEach(() => {
    statusUrls = [];
    appState.spectrogramDefaults = { size: 'lg', raw: false };
    settingsStore.set(initialStore);

    class VisibleIntersectionObserver implements IntersectionObserver {
      readonly root: Element | null = null;
      readonly rootMargin = '';
      readonly scrollMargin = '';
      readonly thresholds: ReadonlyArray<number> = [];
      private readonly cb: IntersectionObserverCallback;
      constructor(cb: IntersectionObserverCallback) {
        this.cb = cb;
      }
      observe = vi.fn((target: Element) => {
        const rect = target.getBoundingClientRect();
        const entry: IntersectionObserverEntry = {
          boundingClientRect: rect,
          intersectionRatio: 1,
          intersectionRect: rect,
          isIntersecting: true,
          rootBounds: null,
          target,
          time: 0,
        };
        this.cb([entry], this);
      });
      unobserve = vi.fn();
      disconnect = vi.fn();
      takeRecords = vi.fn().mockReturnValue([]);
    }
    globalThis.IntersectionObserver = VisibleIntersectionObserver;

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/status?')) {
          statusUrls.push(url);
          return new Response(JSON.stringify({ data: { status: 'exists' } }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return new Response('{}', { status: 200 });
      })
    );
  });

  afterEach(() => {
    globalThis.IntersectionObserver = OriginalIO;
    settingsStore.set(initialStore);
    appState.spectrogramDefaults = initialAppDefaultsReset(initialAppSpec);
    vi.unstubAllGlobals();
  });

  function initialAppDefaultsReset(val: typeof appState.spectrogramDefaults) {
    return val;
  }

  it('uses /app/config spectrogramDefaults on mount without an initial sm request or spurious restart', async () => {
    render(DetectionCardMobile, { props: { detection: createDetection() } });
    await tick();
    await Promise.resolve();

    expect(statusUrls).toEqual(['/api/v2/spectrogram/655/status?size=lg&raw=false']);

    // When /api/v2/settings resolves with matching values, no duplicate restart occurs.
    seedSavedRealtime({
      dashboard: {
        spectrogram: { ...DEFAULT_SPECTROGRAM_SETTINGS, size: 'lg', raw: false },
      },
    });
    await tick();
    await Promise.resolve();

    expect(statusUrls).toEqual(['/api/v2/spectrogram/655/status?size=lg&raw=false']);

    // Updating size/raw in settings restarts the loader with the new values.
    seedSavedRealtime({
      dashboard: {
        spectrogram: { ...DEFAULT_SPECTROGRAM_SETTINGS, size: 'xl', raw: true },
      },
    });
    await tick();
    await Promise.resolve();

    expect(statusUrls).toEqual([
      '/api/v2/spectrogram/655/status?size=lg&raw=false',
      '/api/v2/spectrogram/655/status?size=xl&raw=true',
    ]);

    // Unsaved formData edits must not restart the loader.
    settingsStore.update(
      store =>
        ({
          ...store,
          formData: {
            ...store.formData,
            realtime: {
              ...store.formData.realtime,
              dashboard: {
                ...store.formData.realtime?.dashboard,
                spectrogram: { ...DEFAULT_SPECTROGRAM_SETTINGS, size: 'sm', raw: false },
              },
            },
          },
        }) as unknown as typeof store
    );
    await tick();
    await Promise.resolve();

    expect(statusUrls).toEqual([
      '/api/v2/spectrogram/655/status?size=lg&raw=false',
      '/api/v2/spectrogram/655/status?size=xl&raw=true',
    ]);
  });
});

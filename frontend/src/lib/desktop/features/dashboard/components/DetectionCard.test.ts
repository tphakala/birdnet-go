import { makeBatWavBuffer } from '../../../../../test/audioFixtures';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import DetectionCard from './DetectionCard.svelte';
import type { Detection } from '$lib/types/detection.types';
import { appState } from '$lib/stores/appState.svelte';
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
    id: 654,
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
    clipName: '',
    ...overrides,
  };
}

describe('DetectionCard spectrogram-only detection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the kept spectrogram image without play controls', () => {
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ spectrogramOnly: true }) },
    });

    const img = container.querySelector('img.spectrogram-img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toContain('/api/v2/spectrogram/654?size=md&raw=true');
    expect(container.querySelector('.detection-card-inner.compact')).toBeNull();
    // Audio-only controls stay hidden: no play overlay and no audio settings button.
    expect(screen.queryByRole('button', { name: /play/i })).toBeNull();
  });

  it('shows the play control for a detection with audio', () => {
    render(DetectionCard, { props: { detection: createDetection({ clipName: 'clip_654.wav' }) } });

    // Positive control for the play-button selector used by the image-only test.
    expect(screen.getAllByRole('button', { name: /play/i }).length).toBeGreaterThan(0);
  });

  it('shows the unavailable state when the kept image fails to load', async () => {
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ spectrogramOnly: true }) },
    });

    const img = container.querySelector('img.spectrogram-img');
    expect(img).not.toBeNull();
    if (img) await fireEvent.error(img);

    expect(container.querySelector('img.spectrogram-img')).toBeNull();
    expect(container.querySelector('.spectrogram-unavailable')).not.toBeNull();
  });

  it('keeps the compact layout when there is neither audio nor a kept image', () => {
    const { container } = render(DetectionCard, { props: { detection: createDetection() } });

    expect(container.querySelector('.detection-card-inner.compact')).not.toBeNull();
    expect(container.querySelector('img.spectrogram-img')).toBeNull();
  });
});

describe('DetectionCard playback denoise default', () => {
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
    render(DetectionCard, { props: { detection: createDetection({ clipName: 'clip_654.wav' }) } });
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
    let batsUrls: string[];
    let capturingBats: boolean;

    beforeEach(() => {
      batsUrls = [];
      capturingBats = false;
      restoreProbe = mockAudioProbe({ status: SERVER_ERROR_STATUS });
      probe = globalThis.fetch;
      const wrapper = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (!capturingBats) return probe(input, init);
        batsUrls.push(String(input));
        return new Response(makeBatWavBuffer(), { status: 200 });
      });
      globalThis.fetch = wrapper;
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:bats');
    });

    afterEach(() => {
      globalThis.fetch = probe;
      restoreProbe?.();
      vi.restoreAllMocks();
    });

    async function requestBats(): Promise<Array<string | null>> {
      render(DetectionCard, {
        props: {
          detection: createDetection({ clipName: 'clip_654.wav', modelType: 'bat' }),
        },
      });
      ctl.completeLoad();
      await fireEvent.click(screen.getAllByRole('button', { name: /play/i })[0]);
      await settle();
      return batsUrls.map(u => new URL(u, 'http://localhost').searchParams.get('denoise'));
    }

    async function enableBats() {
      capturingBats = true;
      await fireEvent.click(screen.getByLabelText('media.audio.audibleBats.title'));
      await fireEvent.click(screen.getByText('media.audio.audibleBats.enable'));
      await settle();
    }

    it('sends the playback default', async () => {
      await requestBats();
      await enableBats();

      expect(batsUrls.map(u => new URL(u, 'http://localhost').searchParams.get('denoise'))).toEqual(
        [PRESET]
      );
    });

    it('sends no denoise once the denoised clip failed', async () => {
      await requestBats();
      ctl.failLoad();
      await settle();
      await enableBats();

      expect(batsUrls.map(u => new URL(u, 'http://localhost').searchParams.get('denoise'))).toEqual(
        [null]
      );
    });
  });
});

describe('DetectionCard playback gain and high-pass defaults', () => {
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
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ clipName: 'clip_654.wav' }) },
    });
    const trigger = screen.getByRole('button', { name: SETTINGS_BUTTON });
    expect(container.querySelector('.settings-indicator')).toBeNull();

    await fireEvent.click(trigger);

    expect(sliders(container).gain.value).toBe(String(DEFAULT_GAIN_DB));
    expect(sliders(container).filter.value).toBe(String(DEFAULT_CUTOFF_HZ));
  });

  it('shows the dot once the filter moves and Reset returns to the defaults', async () => {
    const { container } = render(DetectionCard, {
      props: { detection: createDetection({ clipName: 'clip_654.wav' }) },
    });
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

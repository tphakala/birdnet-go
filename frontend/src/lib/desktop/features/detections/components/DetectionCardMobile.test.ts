import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import DetectionCardMobile from './DetectionCardMobile.svelte';
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

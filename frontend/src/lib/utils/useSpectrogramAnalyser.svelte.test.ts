/* global $effect */
import { describe, it, expect, vi } from 'vitest';
import { getAudioContext } from './audioContextManager';
import { createAudioNodeChain } from './audioNodes';
import { useSpectrogramAnalyser } from './useSpectrogramAnalyser.svelte';

vi.mock('./audioContextManager', () => ({
  getAudioContext: vi.fn(),
  isAudioContextSupported: vi.fn(() => true),
  releaseAudioContext: vi.fn(),
}));

const SAMPLE_RATE_HZ = 48000;
const ANALYSER_BIN_COUNT = 512;

/** Fake node whose connect() returns its argument so chained connects work. */
function makeNode() {
  return {
    connect: vi.fn((next: unknown) => next),
    disconnect: vi.fn(),
    gain: { value: 0 },
    frequency: { value: 0 },
    Q: { value: 0 },
    type: '',
    fftSize: 0,
    smoothingTimeConstant: 0,
    frequencyBinCount: ANALYSER_BIN_COUNT,
  };
}

describe('useSpectrogramAnalyser source node', () => {
  it('is the same source node the audio node chain uses for one element and context', async () => {
    const ctx = {
      state: 'running',
      sampleRate: SAMPLE_RATE_HZ,
      destination: {},
      createMediaElementSource: vi.fn(() => makeNode()),
      createGain: vi.fn(() => makeNode()),
      createBiquadFilter: vi.fn(() => makeNode()),
      createAnalyser: vi.fn(() => makeNode()),
    };
    // Partial fake: only the context members the analyser and the chain call exist.
    const audioContext = ctx as unknown as AudioContext;
    vi.mocked(getAudioContext).mockResolvedValue(audioContext);
    // Partial fake: the fake context never reads the element, it is only a cache key.
    const element = {} as unknown as HTMLAudioElement;

    let analyser: ReturnType<typeof useSpectrogramAnalyser> | undefined;
    const stop = $effect.root(() => {
      analyser = useSpectrogramAnalyser();
    });
    await analyser?.connect(element);
    const chain = createAudioNodeChain(audioContext, element);
    stop();

    expect(ctx.createMediaElementSource).toHaveBeenCalledTimes(1);
    expect(chain.source).toBe(ctx.createMediaElementSource.mock.results.at(0)?.value);
  });
});

/**
 * Tests for audioNodes utilities, focused on attachAudioGraphWhenRunning.
 *
 * This helper encapsulates the iOS-critical decision shared by all three audio
 * engines (useAudioPlayback, AudioPlayer, PlayOverlay): the Web Audio graph
 * must only be attached when the AudioContext is actually running, because
 * createMediaElementSource permanently reroutes the element's output and a
 * suspended context plays silently on iOS.
 */

import { describe, it, expect, vi } from 'vitest';
import { attachAudioGraphWhenRunning, type AudioNodeChain } from './audioNodes';

/**
 * Minimal fake AudioContext. connect() returns its argument so the
 * source -> highPass -> gain -> destination chaining in createAudioNodeChain
 * works, and every node-factory method records calls for assertions.
 */
function makeFakeContext(state: AudioContextState) {
  const makeNode = () => ({
    connect: vi.fn((next: unknown) => next),
    disconnect: vi.fn(),
    gain: { value: 0 },
    frequency: { value: 0 },
    Q: { value: 0 },
    type: '',
    threshold: { value: 0 },
    knee: { value: 0 },
    ratio: { value: 0 },
    attack: { value: 0 },
    release: { value: 0 },
  });
  const ctx = {
    state,
    destination: {},
    createMediaElementSource: vi.fn(() => makeNode()),
    createGain: vi.fn(() => makeNode()),
    createBiquadFilter: vi.fn(() => makeNode()),
    createDynamicsCompressor: vi.fn(() => makeNode()),
  };
  return ctx as unknown as AudioContext & { createMediaElementSource: ReturnType<typeof vi.fn> };
}

const fakeElement = {} as HTMLAudioElement;

describe('attachAudioGraphWhenRunning', () => {
  it('builds and returns the graph when the context is running', () => {
    const ctx = makeFakeContext('running');

    const chain = attachAudioGraphWhenRunning(ctx, fakeElement, null);

    expect(chain).not.toBeNull();
    expect(ctx.createMediaElementSource).toHaveBeenCalledTimes(1);
  });

  it('returns null and does NOT build the graph when the context is suspended (iOS)', () => {
    const ctx = makeFakeContext('suspended');

    const chain = attachAudioGraphWhenRunning(ctx, fakeElement, null);

    // The element must play through its native output instead; routing it
    // through a suspended context would silence it on iOS.
    expect(chain).toBeNull();
    expect(ctx.createMediaElementSource).not.toHaveBeenCalled();
  });

  it('returns null when the context is closed', () => {
    const ctx = makeFakeContext('closed');

    expect(attachAudioGraphWhenRunning(ctx, fakeElement, null)).toBeNull();
    expect(ctx.createMediaElementSource).not.toHaveBeenCalled();
  });

  it('returns null when the context is null', () => {
    expect(attachAudioGraphWhenRunning(null, fakeElement, null)).toBeNull();
  });

  it('returns null when the audio element is null', () => {
    const ctx = makeFakeContext('running');

    expect(attachAudioGraphWhenRunning(ctx, null, null)).toBeNull();
    expect(ctx.createMediaElementSource).not.toHaveBeenCalled();
  });

  it('returns the existing chain unchanged without rebuilding (once-per-element guard)', () => {
    const ctx = makeFakeContext('running');
    const existing = { source: {}, gain: {}, highPass: {} } as unknown as AudioNodeChain;

    const chain = attachAudioGraphWhenRunning(ctx, fakeElement, existing);

    // Must not call createMediaElementSource a second time for the same element.
    expect(chain).toBe(existing);
    expect(ctx.createMediaElementSource).not.toHaveBeenCalled();
  });

  it('passes the compressor option through to the built chain', () => {
    const ctx = makeFakeContext('running');

    attachAudioGraphWhenRunning(ctx, fakeElement, null, { includeCompressor: true });

    expect(ctx.createDynamicsCompressor).toHaveBeenCalledTimes(1);
  });
});

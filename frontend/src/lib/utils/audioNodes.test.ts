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
import {
  attachAudioGraphWhenRunning,
  createAudioNodeChain,
  getOrCreateMediaElementSource,
  type AudioNodeChain,
} from './audioNodes';

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
  // Partial fake: only the factory methods the chain calls exist.
  return ctx as unknown as AudioContext & { createMediaElementSource: ReturnType<typeof vi.fn> };
}

// Placeholder element: the fake context never reads it
const fakeElement = {} as unknown as HTMLAudioElement;

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
    // Partial fake: the helper only returns the chain, it never reads its nodes.
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

describe('createAudioNodeChain failure handling', () => {
  // A fresh element per test: the source-node cache is keyed by element.
  // Partial fake: the fake context never reads the element, it is only a cache key.
  const freshElement = () => ({}) as unknown as HTMLAudioElement;

  /** Make the next createGain() throw, as a failed graph build would. */
  function failNextGraphBuild(ctx: ReturnType<typeof makeFakeContext>) {
    const createGain = vi.mocked(ctx.createGain);
    createGain.mockImplementationOnce(() => {
      throw new Error('graph build failed');
    });
  }

  /** The source node the fake context handed out for the nth createMediaElementSource call. */
  function sourceNode(ctx: ReturnType<typeof makeFakeContext>, call: number) {
    const result = ctx.createMediaElementSource.mock.results.at(call);
    // The fake context returns plain mock nodes, so narrow to the two methods used.
    return result?.value as {
      connect: ReturnType<typeof vi.fn>;
      disconnect: ReturnType<typeof vi.fn>;
    };
  }

  it('reuses the element source node when an earlier graph build failed', () => {
    const ctx = makeFakeContext('running');
    const element = freshElement();
    failNextGraphBuild(ctx);

    expect(() => createAudioNodeChain(ctx, element)).toThrow('graph build failed');
    const chain = createAudioNodeChain(ctx, element);

    // A second createMediaElementSource on the same element throws
    // InvalidStateError in a browser, so the first source must be reused.
    expect(ctx.createMediaElementSource).toHaveBeenCalledTimes(1);
    expect(chain.source).toBe(sourceNode(ctx, 0));
  });

  it('routes the source straight to the output when the graph build fails', () => {
    const ctx = makeFakeContext('running');
    failNextGraphBuild(ctx);

    expect(() => createAudioNodeChain(ctx, freshElement())).toThrow('graph build failed');

    expect(sourceNode(ctx, 0).connect).toHaveBeenCalledWith(ctx.destination);
  });

  it('drops the fallback route before rebuilding the graph', () => {
    const ctx = makeFakeContext('running');
    const element = freshElement();
    failNextGraphBuild(ctx);
    expect(() => createAudioNodeChain(ctx, element)).toThrow('graph build failed');
    const source = sourceNode(ctx, 0);
    const fallbackConnectOrder = source.connect.mock.invocationCallOrder.at(-1) ?? 0;

    createAudioNodeChain(ctx, element);

    const disconnectOrder = source.disconnect.mock.invocationCallOrder.at(-1) ?? 0;
    const rebuildConnectOrder = source.connect.mock.invocationCallOrder.at(-1) ?? 0;
    expect(disconnectOrder).toBeGreaterThan(fallbackConnectOrder);
    expect(rebuildConnectOrder).toBeGreaterThan(disconnectOrder);
  });
});

describe('getOrCreateMediaElementSource', () => {
  // Partial fake: the fake context never reads the element, it is only a cache key.
  const freshElement = () => ({}) as unknown as HTMLAudioElement;

  it('returns one source per context and element', () => {
    const ctx = makeFakeContext('running');
    const element = freshElement();

    const first = getOrCreateMediaElementSource(ctx, element);
    const second = getOrCreateMediaElementSource(ctx, element);

    expect(second.source).toBe(first.source);
    expect(first.cached).toBe(false);
    expect(second.cached).toBe(true);
    expect(ctx.createMediaElementSource).toHaveBeenCalledTimes(1);
  });

  it('creates a separate source for another context', () => {
    const element = freshElement();
    const ctxA = makeFakeContext('running');
    const ctxB = makeFakeContext('running');

    const a = getOrCreateMediaElementSource(ctxA, element);
    const b = getOrCreateMediaElementSource(ctxB, element);

    expect(b.source).not.toBe(a.source);
  });

  it('is shared with the node chain so one element never gets two sources', () => {
    const ctx = makeFakeContext('running');
    const element = freshElement();

    const { source } = getOrCreateMediaElementSource(ctx, element);
    const chain = createAudioNodeChain(ctx, element);

    expect(chain.source).toBe(source);
    expect(ctx.createMediaElementSource).toHaveBeenCalledTimes(1);
  });
});

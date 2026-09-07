import { afterEach, describe, expect, it, vi } from 'vitest';
import { generateSessionId } from './session';

// RFC 4122 v4 UUID: 8-4-4-4-12 hex with version nibble 4 and variant nibble 8..b.
const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function fillDeterministic(seed: number): (arr: Uint8Array) => Uint8Array {
  // Mutate in place like the real crypto.getRandomValues (production reads the
  // passed-in array, not the return value). set(map(...)) avoids computed-index
  // writes so it does not trip the object-injection lint rule.
  return (arr: Uint8Array): Uint8Array => {
    arr.set(arr.map((_, i) => (i * seed + 11) & 0xff));
    return arr;
  };
}

describe('generateSessionId', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('returns the native crypto.randomUUID value in a secure context', () => {
    // Pin the native branch: assert it is the one used, not merely that some path
    // returned a UUID (the env would otherwise decide this silently).
    const sentinel = '11111111-1111-4111-8111-111111111111';
    const randomUUID = vi.fn((): string => sentinel);
    vi.stubGlobal('crypto', { randomUUID });

    const id = generateSessionId();
    expect(randomUUID).toHaveBeenCalledTimes(1);
    expect(id).toBe(sentinel);
    expect(id).toMatch(UUID_V4);
  });

  it('returns a valid v4 UUID via getRandomValues when randomUUID is unavailable (plain HTTP)', () => {
    // Simulate a non-secure context: randomUUID missing, getRandomValues present.
    const getRandomValues = vi.fn(fillDeterministic(37));
    vi.stubGlobal('crypto', { getRandomValues });

    const id = generateSessionId();
    // Pin the getRandomValues branch so a fall-through to Math.random (which also
    // yields a valid UUID) cannot masquerade as this path.
    expect(getRandomValues).toHaveBeenCalledTimes(1);
    expect(id).toMatch(UUID_V4);
  });

  it('returns a valid v4 UUID via Math.random when Web Crypto is entirely absent', () => {
    vi.stubGlobal('crypto', undefined);
    const mathRandom = vi.spyOn(Math, 'random');

    const id = generateSessionId();
    expect(mathRandom).toHaveBeenCalled();
    expect(id).toMatch(UUID_V4);
  });

  it('falls back to getRandomValues when randomUUID throws (secure-context guard)', () => {
    const getRandomValues = vi.fn(fillDeterministic(13));
    vi.stubGlobal('crypto', {
      randomUUID: (): string => {
        throw new Error('not a secure context');
      },
      getRandomValues,
    });

    const id = generateSessionId();
    expect(getRandomValues).toHaveBeenCalledTimes(1);
    expect(id).toMatch(UUID_V4);
  });

  it('produces unique identifiers across calls', () => {
    const ids = new Set(Array.from({ length: 100 }, () => generateSessionId()));
    expect(ids.size).toBe(100);
  });
});

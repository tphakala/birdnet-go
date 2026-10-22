/**
 * Test helpers for the one byte audio probe (probeAudioSource in $lib/utils/audio).
 * The probe is the only fetch that sends a Range header, so it is told apart from
 * the other requests a component makes by that header.
 */

import { vi } from 'vitest';

/** One mocked probe answer. */
export interface ProbeAnswer {
  status: number;
  headers?: Record<string, string>;
  /** Delay the answer by this many fake-timer ms (needs vi.useFakeTimers). */
  delayMs?: number;
}

type FetchImpl = typeof globalThis.fetch;

/**
 * Answers every ranged request with `answer` and defers all others to the
 * implementation that was installed before. Returns a restore function for afterEach.
 */
export function mockAudioProbe(answer: ProbeAnswer): () => void {
  const fetchMock = vi.mocked(globalThis.fetch);
  const original = fetchMock.getMockImplementation();
  fetchMock.mockImplementation(((input: RequestInfo | URL, init?: RequestInit) => {
    const range = new Headers(init?.headers).get('Range');
    if (range === null) {
      if (original) return original(input, init);
      return Promise.reject(new Error('unmocked fetch'));
    }
    // Partial Response: the probe reads only status, headers and body.cancel.
    const response = {
      status: answer.status,
      headers: new Headers(answer.headers),
      body: { cancel: () => Promise.resolve() },
    } as unknown as Response;
    if (answer.delayMs === undefined) return Promise.resolve(response);
    return new Promise<Response>((resolve, reject) => {
      const timer = setTimeout(() => {
        resolve(response);
      }, answer.delayMs);
      init?.signal?.addEventListener('abort', () => {
        clearTimeout(timer);
        reject(new DOMException('aborted', 'AbortError'));
      });
    });
  }) satisfies FetchImpl);
  return () => {
    fetchMock.mockReset();
    if (original) fetchMock.mockImplementation(original);
  };
}

/** The ranged requests sent so far, as [url, init] pairs. */
export function probeCalls(): [string, RequestInit][] {
  const calls: [string, RequestInit][] = [];
  for (const [input, init] of vi.mocked(globalThis.fetch).mock.calls) {
    if (init && new Headers(init.headers).get('Range') !== null) {
      calls.push([String(input), init]);
    }
  }
  return calls;
}

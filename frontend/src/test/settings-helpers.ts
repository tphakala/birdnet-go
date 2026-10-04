/**
 * Shared helpers for settings store and wizard step tests.
 */

import type { SettingsFormData } from '$lib/stores/settings';

/** A promise with its resolve and reject exposed, for tests that control timing. */
export interface Deferred<T = void> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

/** Creates a Deferred. The target is ES2022, so Promise.withResolvers is unavailable. */
export function deferred<T = void>(): Deferred<T> {
  let resolve: (value: T) => void = () => {};
  let reject: (err: Error) => void = () => {};
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Reads the value at path in a nested object, or undefined when any segment is missing. */
export function lookup(root: unknown, path: string[]): unknown {
  let current: unknown = root;
  for (const segment of path) {
    if (current === null || typeof current !== 'object') return undefined;
    const record = current as Record<string, unknown>;
    // eslint-disable-next-line security/detect-object-injection -- test helper with fixed paths
    current = Object.hasOwn(record, segment) ? record[segment] : undefined;
  }
  return current;
}

/**
 * A server settings response with distinct values in every section the wizard
 * patches, plus nested values a section save must not lose. Each call returns a
 * fresh object, so tests may adjust it before handing it to a mock.
 */
export const serverSettings = () =>
  ({
    main: { name: 'TestNode' },
    birdnet: {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.8,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 0,
      longitude: 0,
      locationConfigured: false,
      rangeFilter: {
        threshold: 0.03,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
    },
    realtime: {
      dashboard: { summaryLimit: 100, locale: 'en' },
      audio: {
        source: 'old-device',
        sources: [{ name: 'Card', device: 'hw:0' }],
        equalizer: {
          enabled: true,
          filters: [{ type: 'HighPass', frequency: 200, q: 0.7, passes: 1 }],
        },
        export: { enabled: true, type: 'wav' },
      },
      rtsp: {
        streams: [{ name: 'Old', url: 'rtsp://old', enabled: true, type: 'rtsp' }],
        health: { healthyDataThreshold: 60 },
        ffmpegParameters: ['-x'],
      },
      privacyFilter: { enabled: false, confidence: 0.7, debug: true, vad: { enabled: true } },
      birdweather: { enabled: false, id: '', threshold: 0.9, debug: true },
    },
    sentry: { enabled: false },
  }) as unknown as SettingsFormData;

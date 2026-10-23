/**
 * Fake HTMLMediaElement for audio recovery tests.
 *
 * jsdom does not implement media loading or playback, so this patches
 * HTMLMediaElement.prototype with the parts of the WHATWG media element model
 * that the retry and recovery code depends on:
 *
 * - `src` assignment and `load()` run the load algorithm: they set `paused` to
 *   true WITHOUT a `pause` event, reject pending `play()` promises with
 *   AbortError, clear `error` and fire `loadstart`.
 * - `play()` on an element whose `error.code` is 4 rejects with
 *   NotSupportedError; otherwise it unpauses, fires `play` and resolves (or
 *   stays pending in 'hold' mode until `completeLoad()`).
 * - `pause()` on a playing element rejects pending plays and fires `pause`.
 *
 * Event dispatch is synchronous. Settle promises with
 * `await vi.advanceTimersByTimeAsync(0)`. jsdom's real addEventListener and
 * dispatchEvent are kept, so components attach listeners as in production.
 */

import { vi, type MockInstance } from 'vitest';
import type { AudioNodeChain } from '$lib/utils/audioNodes';

/** MediaError.MEDIA_ERR_NETWORK */
export const MEDIA_ERR_NETWORK = 2;

/** MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED, which browsers report for HTTP 503 and 404 */
const MEDIA_ERR_SRC_NOT_SUPPORTED = 4;

/** Duration reported by the fake element once metadata has loaded, in seconds */
export const FAKE_MEDIA_DURATION_SECONDS = 120;

/** How a `play()` call settles: immediately, or only when `completeLoad()` runs */
type FakePlayMode = 'resolve' | 'hold';

/** Names of DOMException rejections the fake can queue for the next `play()` */
type FakePlayRejection = 'NotAllowedError' | 'NotSupportedError' | 'AbortError';

interface FakeMediaError {
  code: number;
  message: string;
}

interface PendingPlay {
  resolve: () => void;
  reject: (reason: DOMException) => void;
}

interface ElementState {
  paused: boolean;
  error: FakeMediaError | null;
  currentTime: number;
  pending: PendingPlay[];
  mode: FakePlayMode;
  nextRejection: FakePlayRejection | null;
}

/** Handle returned by installFakeMediaElement */
export interface FakeMediaController {
  /** The single element that `new Audio()` returns while the fake is installed */
  readonly element: HTMLAudioElement;
  /** Whether the element is currently paused */
  readonly paused: boolean;
  /** The element's current MediaError-like object, or null */
  readonly error: FakeMediaError | null;
  /** Spy on HTMLMediaElement.prototype.play */
  readonly play: MockInstance;
  /** Spy on HTMLMediaElement.prototype.load */
  readonly load: MockInstance;
  /** Fire an event on the element */
  dispatch(type: string): void;
  /** Make the next play() reject with the named DOMException */
  rejectNextPlay(name: FakePlayRejection): void;
  /** Choose whether play() resolves at once or stays pending until completeLoad() */
  setPlayMode(mode: FakePlayMode): void;
  /** Fail the load: set the MediaError, fire `error`, reject pending plays */
  failLoad(code?: number): void;
  /** Finish the load: fire `loadedmetadata`, then `canplay`, resolve pending plays */
  completeLoad(options?: { canplay?: boolean }): void;
  /** Put every patched property back and release the Audio stub */
  restore(): void;
}

const PATCHED_PROPERTIES = [
  'paused',
  'error',
  'src',
  'duration',
  'currentTime',
  'play',
  'pause',
  'load',
] as const;

/**
 * Install the fake on HTMLMediaElement.prototype and stub `window.Audio` to
 * return one audio element. Call `restore()` in afterEach: vi.restoreAllMocks
 * does not undo defineProperty.
 */
export function installFakeMediaElement(): FakeMediaController {
  const proto = window.HTMLMediaElement.prototype;
  const saved = new Map<string, PropertyDescriptor | undefined>();
  for (const name of PATCHED_PROPERTIES) {
    saved.set(name, Object.getOwnPropertyDescriptor(proto, name));
  }
  const originalSrcDescriptor = saved.get('src');

  const states = new WeakMap<HTMLMediaElement, ElementState>();
  const stateOf = (el: HTMLMediaElement): ElementState => {
    let state = states.get(el);
    if (!state) {
      state = {
        paused: true,
        error: null,
        currentTime: 0,
        pending: [],
        mode: 'resolve',
        nextRejection: null,
      };
      states.set(el, state);
    }
    return state;
  };

  const rejectPending = (state: ElementState, name: string) => {
    const pending = state.pending;
    state.pending = [];
    for (const p of pending) {
      p.reject(new DOMException(`play() rejected: ${name}`, name));
    }
  };

  // The load algorithm: paused becomes true without a pause event.
  const runLoadAlgorithm = (el: HTMLMediaElement) => {
    const state = stateOf(el);
    rejectPending(state, 'AbortError');
    state.paused = true;
    state.error = null;
    el.dispatchEvent(new Event('loadstart'));
  };

  const play = vi.fn(function (this: HTMLMediaElement): Promise<void> {
    const state = stateOf(this);
    if (state.error?.code === MEDIA_ERR_SRC_NOT_SUPPORTED) {
      return Promise.reject(
        new DOMException('The element has no supported sources', 'NotSupportedError')
      );
    }
    if (state.nextRejection) {
      const name = state.nextRejection;
      state.nextRejection = null;
      return Promise.reject(new DOMException(`play() rejected: ${name}`, name));
    }
    if (state.paused) {
      state.paused = false;
      this.dispatchEvent(new Event('play'));
    }
    if (state.mode === 'hold') {
      return new Promise<void>((resolve, reject) => {
        state.pending.push({ resolve, reject });
      });
    }
    return Promise.resolve();
  });

  const pause = vi.fn(function (this: HTMLMediaElement): void {
    const state = stateOf(this);
    if (state.paused) return;
    state.paused = true;
    rejectPending(state, 'AbortError');
    this.dispatchEvent(new Event('pause'));
  });

  const load = vi.fn(function (this: HTMLMediaElement): void {
    runLoadAlgorithm(this);
  });

  Object.defineProperty(proto, 'play', { configurable: true, writable: true, value: play });
  Object.defineProperty(proto, 'pause', { configurable: true, writable: true, value: pause });
  Object.defineProperty(proto, 'load', { configurable: true, writable: true, value: load });
  Object.defineProperty(proto, 'paused', {
    configurable: true,
    get(this: HTMLMediaElement) {
      return stateOf(this).paused;
    },
  });
  Object.defineProperty(proto, 'error', {
    configurable: true,
    get(this: HTMLMediaElement) {
      return stateOf(this).error;
    },
  });
  Object.defineProperty(proto, 'duration', {
    configurable: true,
    get() {
      return FAKE_MEDIA_DURATION_SECONDS;
    },
  });
  Object.defineProperty(proto, 'currentTime', {
    configurable: true,
    get(this: HTMLMediaElement) {
      return stateOf(this).currentTime;
    },
    set(this: HTMLMediaElement, value: number) {
      stateOf(this).currentTime = value;
    },
  });
  Object.defineProperty(proto, 'src', {
    configurable: true,
    get(this: HTMLMediaElement) {
      return originalSrcDescriptor?.get?.call(this) ?? '';
    },
    set(this: HTMLMediaElement, value: string) {
      originalSrcDescriptor?.set?.call(this, value);
      runLoadAlgorithm(this);
    },
  });

  const element = document.createElement('audio');
  const audioSpy = vi.spyOn(window, 'Audio').mockImplementation(function () {
    return element;
  });
  const state = stateOf(element);

  return {
    element,
    get paused() {
      return state.paused;
    },
    get error() {
      return state.error;
    },
    play,
    load,
    dispatch(type: string) {
      element.dispatchEvent(new Event(type));
    },
    rejectNextPlay(name: FakePlayRejection) {
      state.nextRejection = name;
    },
    setPlayMode(mode: FakePlayMode) {
      state.mode = mode;
    },
    failLoad(code: number = MEDIA_ERR_SRC_NOT_SUPPORTED) {
      state.error = { code, message: 'fake media error' };
      element.dispatchEvent(new Event('error'));
      rejectPending(state, 'NotSupportedError');
    },
    completeLoad(options: { canplay?: boolean } = {}) {
      const { canplay = true } = options;
      element.dispatchEvent(new Event('loadedmetadata'));
      if (canplay) element.dispatchEvent(new Event('canplay'));
      const pending = state.pending;
      state.pending = [];
      for (const p of pending) p.resolve();
    },
    restore() {
      audioSpy.mockRestore();
      for (const name of PATCHED_PROPERTIES) {
        const descriptor = saved.get(name);
        if (descriptor) {
          Object.defineProperty(proto, name, descriptor);
        } else {
          Reflect.deleteProperty(proto, name);
        }
      }
    },
  };
}

/**
 * Let pending timers and promise callbacks run without advancing the clock.
 * Requires vi.useFakeTimers().
 */
export async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

/** Minimal Web Audio node chain for tests that mock attachAudioGraphWhenRunning. */
export function fakeAudioNodeChain(): AudioNodeChain {
  return {
    source: { connect: vi.fn(), disconnect: vi.fn() },
    gain: { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() },
    highPass: { frequency: { value: 20 }, connect: vi.fn(), disconnect: vi.fn() },
  } as unknown as AudioNodeChain; // minimal node fakes: tests never call real Web Audio
}

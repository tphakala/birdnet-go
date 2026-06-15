/**
 * Tests for useAudioPlayback composable.
 *
 * Uses a thin Svelte wrapper component to provide the onMount context
 * required by the composable. Follows the same Audio mocking pattern
 * established in AudioPlayer.test.ts.
 */

/* global Audio */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { waitFor, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../test/render-helpers';
import { MAX_AUDIO_LOAD_RETRIES, AUDIO_RETRY_DELAY_MS } from '$lib/utils/audio';
import { safeGet } from '$lib/utils/security';
import type { AudioPlaybackState } from './useAudioPlayback.svelte';
import Wrapper from './UseAudioPlaybackWrapper.test.svelte';

// Mock audioContextManager - must be before import.
// Default to a 'running' context (the resumed/desktop case). Individual tests
// override with a 'suspended' context to exercise the iOS code path.
vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({
    state: 'running',
    createMediaElementSource: vi.fn().mockReturnValue({
      connect: vi.fn(),
      disconnect: vi.fn(),
    }),
    createGain: vi.fn().mockReturnValue({
      gain: { value: 1 },
      connect: vi.fn(),
      disconnect: vi.fn(),
    }),
    createBiquadFilter: vi.fn().mockReturnValue({
      type: 'highpass',
      frequency: { value: 20 },
      connect: vi.fn(),
      disconnect: vi.fn(),
    }),
    destination: {},
  }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

// Mock audioNodes. attachAudioGraphWhenRunning is the composable's direct
// dependency; its own running/suspended decision is unit-tested separately in
// audioNodes.test.ts. Here we control its return value to simulate an attached
// graph (running -> chain) vs a deferred one (suspended -> null).
vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn().mockReturnValue({
    source: { connect: vi.fn(), disconnect: vi.fn() },
    gain: { gain: { value: 1 }, connect: vi.fn(), disconnect: vi.fn() },
    highPass: { frequency: { value: 20 }, connect: vi.fn(), disconnect: vi.fn() },
  }),
  disconnectAudioNodes: vi.fn(),
}));

describe('useAudioPlayback', () => {
  let mockPlay: ReturnType<typeof vi.fn>;
  let mockPause: ReturnType<typeof vi.fn>;
  let mockLoad: ReturnType<typeof vi.fn>;
  const eventHandlers: Record<string, EventListener[]> = {};
  let mockAudioInstance: HTMLAudioElement;
  let capturedState: AudioPlaybackState | null = null;
  const wrapperTest = createComponentTestFactory(Wrapper);

  /**
   * Safely access captured state, throwing a clear error if it is null.
   * Avoids non-null assertions (!) throughout the test file.
   */
  function getState(): AudioPlaybackState {
    if (capturedState === null) {
      throw new Error('capturedState is null — did you forget to await waitFor?');
    }
    return capturedState;
  }

  /** Helper to fire a synthetic event on the mock audio element */
  function fireAudioEvent(eventName: string) {
    const handlers = safeGet(eventHandlers, eventName, []) as EventListener[];
    handlers.forEach(handler => {
      handler.call(mockAudioInstance, new Event(eventName));
    });
  }

  /** Render the wrapper and capture the composable state */
  function renderComposable(overrides: Record<string, unknown> = {}) {
    const onState = vi.fn((s: AudioPlaybackState) => {
      capturedState = s;
    });

    const result = wrapperTest.render({
      options: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
        ...overrides,
      },
      onState,
    });

    return { ...result, onState };
  }

  /** Render and wait for the composable to be initialized */
  async function renderAndWait(overrides: Record<string, unknown> = {}) {
    const result = renderComposable(overrides);
    await waitFor(() => {
      expect(capturedState).not.toBeNull();
    });
    return result;
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    capturedState = null;

    // Mock HTMLMediaElement methods
    mockPlay = vi.fn().mockResolvedValue(undefined);
    mockPause = vi.fn();
    mockLoad = vi.fn();

    window.HTMLMediaElement.prototype.play = mockPlay as unknown as () => Promise<void>;
    window.HTMLMediaElement.prototype.pause = mockPause as unknown as () => void;
    window.HTMLMediaElement.prototype.load = mockLoad as unknown as () => void;

    // Store event handlers so tests can fire synthetic events
    window.HTMLMediaElement.prototype.addEventListener = vi.fn(
      (event: string, handler: EventListener) => {
        const handlers = safeGet(eventHandlers, event, []) as EventListener[];
        if (handlers.length === 0) {
          Object.assign(eventHandlers, { [event]: [] });
        }
        (safeGet(eventHandlers, event, []) as EventListener[]).push(handler);
      }
    );

    window.HTMLMediaElement.prototype.removeEventListener = vi.fn(
      (event: string, handler: EventListener) => {
        const handlers = safeGet(eventHandlers, event, []) as EventListener[];
        if (handlers.length > 0) {
          const index = handlers.indexOf(handler);
          if (index > -1) {
            handlers.splice(index, 1);
          }
        }
      }
    );

    // Mock audio properties
    Object.defineProperty(window.HTMLMediaElement.prototype, 'paused', {
      configurable: true,
      get: vi.fn().mockReturnValue(true),
    });

    Object.defineProperty(window.HTMLMediaElement.prototype, 'duration', {
      configurable: true,
      get: vi.fn().mockReturnValue(120),
    });

    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: vi.fn().mockReturnValue(0),
      set: vi.fn(),
    });

    // Mock Audio constructor to return our controlled instance
    mockAudioInstance = document.createElement('audio') as HTMLAudioElement;
    vi.spyOn(window, 'Audio').mockImplementation(function (this: HTMLAudioElement) {
      return mockAudioInstance;
    } as unknown as typeof Audio);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
    // Clear event handlers between tests
    Object.keys(eventHandlers).forEach(key => {
      // eslint-disable-next-line security/detect-object-injection
      eventHandlers[key] = [];
    });
    capturedState = null;
  });

  // ---------------------------------------------------------------
  // 1. togglePlayPause() calls audio.play() when paused
  // ---------------------------------------------------------------
  it('togglePlayPause() calls audio.play() when paused', async () => {
    await renderAndWait();

    await getState().togglePlayPause();
    expect(mockPlay).toHaveBeenCalledTimes(1);
  });

  // ---------------------------------------------------------------
  // 1b. iOS: when the graph cannot attach (suspended context -> helper returns
  //     null), the element still plays and EQ availability reads false instead
  //     of being enabled-but-inert (regression: silent iOS playback).
  // ---------------------------------------------------------------
  it('plays natively and reports no audio graph when the context is suspended (iOS)', async () => {
    const { attachAudioGraphWhenRunning } = await import('$lib/utils/audioNodes');
    vi.mocked(attachAudioGraphWhenRunning).mockReturnValueOnce(null);

    await renderAndWait();
    await getState().togglePlayPause();

    // Element plays through its native output (audible on iOS) ...
    expect(mockPlay).toHaveBeenCalledTimes(1);
    // ... and the gain/filter graph is reported unavailable, not active-yet-inert.
    expect(getState().audioContextAvailable).toBe(false);
  });

  // ---------------------------------------------------------------
  // 1c. A running context attaches the graph and reports it available.
  // ---------------------------------------------------------------
  it('attaches the Web Audio graph and reports it available when the context is running', async () => {
    const { attachAudioGraphWhenRunning } = await import('$lib/utils/audioNodes');

    await renderAndWait();
    await getState().togglePlayPause();

    expect(mockPlay).toHaveBeenCalledTimes(1);
    expect(attachAudioGraphWhenRunning).toHaveBeenCalledTimes(1);
    expect(getState().audioContextAvailable).toBe(true);
  });

  // ---------------------------------------------------------------
  // 1d. Graph attachment is deferred until the context resumes on a later tap.
  // ---------------------------------------------------------------
  it('defers graph attachment until the context resumes on a later interaction', async () => {
    const { attachAudioGraphWhenRunning } = await import('$lib/utils/audioNodes');

    // First gesture: helper returns null (context still suspended) -> no graph.
    vi.mocked(attachAudioGraphWhenRunning).mockReturnValueOnce(null);
    await renderAndWait();
    await getState().togglePlayPause();
    expect(getState().audioContextAvailable).toBe(false);

    // Second gesture: default mock returns a chain (context resumed) -> attached.
    await getState().togglePlayPause();
    expect(getState().audioContextAvailable).toBe(true);
  });

  // ---------------------------------------------------------------
  // 1e. Re-attempts resume when a previously-attached context is later
  //     suspended (e.g. tab backgrounding), instead of short-circuiting to
  //     silent playback.
  // ---------------------------------------------------------------
  it('re-attempts context resume when a previously-attached context is suspended', async () => {
    const { getAudioContext } = await import('$lib/utils/audioContextManager');

    await renderAndWait();
    // First play attaches the graph on a running context.
    await getState().togglePlayPause();
    expect(vi.mocked(getAudioContext)).toHaveBeenCalledTimes(1);

    // Simulate the browser suspending the shared context after attachment.
    const ctx = (await vi.mocked(getAudioContext).mock.results[0]?.value) as AudioContext & {
      state: AudioContextState;
    };
    ctx.state = 'suspended';

    // A subsequent play must re-enter initialization (not short-circuit on the
    // already-attached graph) so getAudioContext() resumes the context again.
    await getState().togglePlayPause();
    expect(vi.mocked(getAudioContext)).toHaveBeenCalledTimes(2);
  });

  // ---------------------------------------------------------------
  // 2. togglePlayPause() calls audio.pause() when playing
  // ---------------------------------------------------------------
  it('togglePlayPause() calls audio.pause() when playing', async () => {
    // Start as not paused
    Object.defineProperty(window.HTMLMediaElement.prototype, 'paused', {
      configurable: true,
      get: vi.fn().mockReturnValue(false),
    });

    await renderAndWait();

    await getState().togglePlayPause();
    expect(mockPause).toHaveBeenCalledTimes(1);
  });

  // ---------------------------------------------------------------
  // 3. Progress updates on timeupdate events
  // ---------------------------------------------------------------
  it('updates progress on timeupdate events', async () => {
    let currentTimeValue = 0;
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => currentTimeValue,
      set: (v: number) => {
        currentTimeValue = v;
      },
    });

    await renderAndWait();

    // Fire loadedmetadata so duration is set
    fireAudioEvent('loadedmetadata');

    // Simulate time progress
    currentTimeValue = 30;
    fireAudioEvent('timeupdate');

    await waitFor(() => {
      const state = getState();
      expect(state.currentTime).toBe(30);
      // progress = (30 / 120) * 100 = 25
      expect(state.progress).toBe(25);
    });
  });

  // ---------------------------------------------------------------
  // 4. Duration set on loadedmetadata event
  // ---------------------------------------------------------------
  it('sets duration on loadedmetadata event', async () => {
    await renderAndWait();

    // Initially duration should be 0
    expect(getState().duration).toBe(0);

    // Fire loadedmetadata — the mock returns 120 for duration
    fireAudioEvent('loadedmetadata');

    await waitFor(() => {
      expect(getState().duration).toBe(120);
    });
  });

  // ---------------------------------------------------------------
  // 5. isLoading set/cleared via loadstart/canplay events
  // ---------------------------------------------------------------
  it('sets isLoading on loadstart and clears on canplay', async () => {
    await renderAndWait();

    // Fire loadstart
    fireAudioEvent('loadstart');

    await waitFor(() => {
      expect(getState().isLoading).toBe(true);
    });

    // Fire canplay
    fireAudioEvent('canplay');

    await waitFor(() => {
      expect(getState().isLoading).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // 6. Error retry logic (fires reload up to MAX_AUDIO_LOAD_RETRIES times)
  // ---------------------------------------------------------------
  it('retries audio load up to MAX_AUDIO_LOAD_RETRIES times on error', async () => {
    await renderAndWait();

    // Fire error events and advance timers for each retry
    for (let i = 0; i < MAX_AUDIO_LOAD_RETRIES; i++) {
      fireAudioEvent('error');
      // Advance past the retry delay so the retry timer fires
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS + 100);
    }

    // After MAX_AUDIO_LOAD_RETRIES retries, load() should have been called
    // for each retry attempt
    expect(mockLoad).toHaveBeenCalledTimes(MAX_AUDIO_LOAD_RETRIES);

    // Error state should NOT be set yet (retries haven't been exhausted
    // until the final error fires)
    expect(getState().error).toBeNull();
  });

  // ---------------------------------------------------------------
  // 7. Error state set after retries exhausted
  // ---------------------------------------------------------------
  it('sets error state after all retries are exhausted', async () => {
    await renderAndWait();

    // Exhaust all retries
    for (let i = 0; i < MAX_AUDIO_LOAD_RETRIES; i++) {
      fireAudioEvent('error');
      await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS + 100);
    }

    // Fire one more error — this one exceeds the retry count
    fireAudioEvent('error');

    await waitFor(() => {
      const state = getState();
      expect(state.error).not.toBeNull();
      expect(state.isLoading).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // 7b. togglePlayPause() must not clobber a queued transient retry
  //     with a play error (regression for #954)
  // ---------------------------------------------------------------
  it('does not surface a play error while a transient retry is queued', async () => {
    await renderAndWait();

    // A transient load error (no permanent MediaError code) queues a retry.
    fireAudioEvent('error');
    expect(getState().error).toBeNull();

    // play() rejects (browsers reject play() once the media has errored), but a
    // retry is already pending, so the rejection must stay silent.
    mockPlay.mockRejectedValueOnce(new DOMException('not supported', 'NotSupportedError'));

    await getState().togglePlayPause();

    expect(getState().error).toBeNull();
  });

  // ---------------------------------------------------------------
  // 7c. A play press during a transient retry resumes once 'canplay' fires,
  //     so the play intent is not lost (regression for #954)
  // ---------------------------------------------------------------
  it('resumes playback on canplay after a play press during a transient retry', async () => {
    await renderAndWait();

    // A transient load error queues a retry.
    fireAudioEvent('error');

    // User presses play during the retry; play() rejects and the intent is held.
    mockPlay.mockRejectedValueOnce(new DOMException('not supported', 'NotSupportedError'));
    await getState().togglePlayPause();
    expect(getState().error).toBeNull();

    const callsBefore = mockPlay.mock.calls.length;

    // Once the reloaded clip can play, playback resumes automatically.
    fireAudioEvent('canplay');
    await waitFor(() => {
      expect(mockPlay.mock.calls.length).toBeGreaterThan(callsBefore);
    });
    expect(getState().error).toBeNull();
  });

  // ---------------------------------------------------------------
  // 8. seek() clamps to [0, duration]
  // ---------------------------------------------------------------
  it('seek() clamps time to [0, duration]', async () => {
    const setCurrentTime = vi.fn();
    let currentTimeValue = 0;
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => currentTimeValue,
      set: (v: number) => {
        currentTimeValue = v;
        setCurrentTime(v);
      },
    });

    await renderAndWait();

    // Set duration first via loadedmetadata
    fireAudioEvent('loadedmetadata');

    await waitFor(() => {
      expect(getState().duration).toBe(120);
    });

    // Seek beyond duration — should clamp to duration
    getState().seek(200);
    expect(setCurrentTime).toHaveBeenCalledWith(120);

    // Seek to negative — should clamp to 0
    getState().seek(-10);
    expect(setCurrentTime).toHaveBeenCalledWith(0);

    // Seek within range — should use exact value
    getState().seek(60);
    expect(setCurrentTime).toHaveBeenCalledWith(60);
  });

  // ---------------------------------------------------------------
  // 9. setAudioUrl() resets playback state
  // ---------------------------------------------------------------
  it('setAudioUrl() resets playback state', async () => {
    let currentTimeValue = 30;
    Object.defineProperty(window.HTMLMediaElement.prototype, 'currentTime', {
      configurable: true,
      get: () => currentTimeValue,
      set: (v: number) => {
        currentTimeValue = v;
      },
    });

    await renderAndWait();

    // Simulate some playback state
    fireAudioEvent('loadedmetadata');
    fireAudioEvent('timeupdate');
    fireAudioEvent('play');

    // Change the audio URL
    getState().setAudioUrl('/audio/different.mp3');

    // The $effect that watches audioUrl should reset state
    // We need to flush effects by waiting
    await vi.advanceTimersByTimeAsync(0);

    await waitFor(() => {
      // After URL change, playback state resets
      const state = getState();
      expect(state.isPlaying).toBe(false);
      expect(state.progress).toBe(0);
      expect(state.error).toBeNull();
    });
  });

  // ---------------------------------------------------------------
  // 10. Cleanup removes event listeners and stops audio
  // ---------------------------------------------------------------
  it('cleanup removes event listeners and stops audio', async () => {
    const removeEventListenerSpy = window.HTMLMediaElement.prototype
      .removeEventListener as ReturnType<typeof vi.fn>;

    const { unmount } = await renderAndWait();

    // Start playing so interval is active
    fireAudioEvent('play');

    const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval');

    unmount();

    // Should have removed event listeners
    expect(removeEventListenerSpy).toHaveBeenCalled();

    // Should have paused audio
    expect(mockPause).toHaveBeenCalled();

    // Should have cleared interval
    expect(clearIntervalSpy).toHaveBeenCalled();
  });

  // ---------------------------------------------------------------
  // Additional: play event sets isPlaying to true
  // ---------------------------------------------------------------
  it('sets isPlaying to true on play event', async () => {
    await renderAndWait();

    expect(getState().isPlaying).toBe(false);

    fireAudioEvent('play');

    await waitFor(() => {
      expect(getState().isPlaying).toBe(true);
    });
  });

  // ---------------------------------------------------------------
  // Additional: pause event sets isPlaying to false
  // ---------------------------------------------------------------
  it('sets isPlaying to false on pause event', async () => {
    await renderAndWait();

    // First play
    fireAudioEvent('play');

    await waitFor(() => {
      expect(getState().isPlaying).toBe(true);
    });

    // Then pause
    fireAudioEvent('pause');

    await waitFor(() => {
      expect(getState().isPlaying).toBe(false);
    });
  });

  // ---------------------------------------------------------------
  // Additional: calls onPlayStart/onPlayEnd callbacks
  // ---------------------------------------------------------------
  it('calls onPlayStart on play and onPlayEnd after pause delay', async () => {
    const onPlayStart = vi.fn();
    const onPlayEnd = vi.fn();

    await renderAndWait({ onPlayStart, onPlayEnd });

    // Play should trigger onPlayStart
    fireAudioEvent('play');
    expect(onPlayStart).toHaveBeenCalledTimes(1);

    // Pause should trigger onPlayEnd after PLAY_END_DELAY_MS
    fireAudioEvent('pause');
    expect(onPlayEnd).not.toHaveBeenCalled();

    vi.advanceTimersByTime(3100); // PLAY_END_DELAY_MS = 3000
    expect(onPlayEnd).toHaveBeenCalledTimes(1);
  });
});

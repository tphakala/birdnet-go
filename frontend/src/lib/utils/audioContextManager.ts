/**
 * Singleton AudioContext Manager
 *
 * Browsers enforce a hard limit (~6) on active AudioContext instances.
 * This module provides a shared AudioContext for all audio components,
 * preventing the "Maximum number of AudioContexts reached" error.
 *
 * Usage:
 *   const ctx = await getAudioContext();
 *   // Use ctx for audio processing
 *   // Call releaseAudioContext() in onDestroy (optional, for tracking)
 */

interface WebkitWindow extends Window {
  webkitAudioContext?: typeof AudioContext;
}

let sharedContext: AudioContext | null = null;

/**
 * Get the AudioContext constructor, accounting for vendor prefixes.
 * Returns null if AudioContext is not supported or in SSR context.
 */
function getAudioContextConstructor(): typeof AudioContext | null {
  // SSR guard - window is not available during server-side rendering
  if (typeof window === 'undefined') {
    return null;
  }
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- browser compatibility check
  if (window.AudioContext) {
    return window.AudioContext;
  }
  const webkitContext = (window as WebkitWindow).webkitAudioContext;
  if (webkitContext) {
    return webkitContext;
  }
  return null;
}

/**
 * Get the shared AudioContext instance.
 * Creates a new context if none exists or if the previous one was closed.
 * Automatically resumes suspended contexts (required after user gesture on mobile).
 *
 * @returns Promise resolving to the AudioContext
 * @throws Error if AudioContext is not supported in this browser
 */
export async function getAudioContext(): Promise<AudioContext> {
  const AudioContextClass = getAudioContextConstructor();

  if (!AudioContextClass) {
    throw new Error('AudioContext not supported in this browser');
  }

  // Create new context if none exists or previous was closed
  if (!sharedContext || sharedContext.state === 'closed') {
    sharedContext = new AudioContextClass();
  }

  // Resume if suspended (required for autoplay policies)
  if (sharedContext.state === 'suspended') {
    await sharedContext.resume();
  }

  return sharedContext;
}

/**
 * Check if AudioContext is supported in this browser.
 * Use this for feature detection before attempting audio playback.
 *
 * @returns true if AudioContext is available
 */
export function isAudioContextSupported(): boolean {
  return getAudioContextConstructor() !== null;
}

/**
 * Release reference to the shared AudioContext.
 * Call this in onDestroy for cleanup tracking.
 * Note: The context itself is not closed - it's reused across components.
 */
export function releaseAudioContext(): void {
  // Currently a no-op, but available for future reference counting
  // if we need to track active users and close idle contexts
}

/**
 * Force close the shared AudioContext.
 * Only use this for cleanup during app shutdown or testing.
 * Normal components should use releaseAudioContext() instead.
 */
export async function closeAudioContext(): Promise<void> {
  if (sharedContext && sharedContext.state !== 'closed') {
    await sharedContext.close();
    sharedContext = null;
  }
}

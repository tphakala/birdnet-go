/**
 * Delayed Loading Utility for Svelte 5
 *
 * Provides a reusable pattern for managing loading states with delayed spinner display.
 * This prevents visual flicker for fast-loading content while maintaining feedback
 * for slower operations.
 *
 * @example
 * ```typescript
 * const { loading, showSpinner, setLoading, cleanup } = useDelayedLoading(150);
 *
 * // Start loading
 * setLoading(true);
 *
 * // On success/error
 * setLoading(false);
 *
 * // In template
 * {#if showSpinner}
 *   <div class="loading loading-spinner"></div>
 * {/if}
 * ```
 */

import { onDestroy } from 'svelte';

export interface DelayedLoadingOptions {
  /** Delay in milliseconds before showing spinner (default: 150ms) */
  delayMs?: number;
  /** Timeout in milliseconds before considering load failed (default: 15000ms) */
  timeoutMs?: number;
  /** Callback when timeout is reached */
  onTimeout?: () => void;
}

export interface DelayedLoadingState {
  /** Whether the resource is currently loading */
  loading: boolean;
  /** Whether to show the loading spinner (delayed) */
  showSpinner: boolean;
  /** Whether an error has occurred */
  error: boolean;
}

/**
 * Creates a delayed loading state manager for preventing spinner flicker
 * on fast-loading content.
 *
 * @param options Configuration options for delayed loading behavior
 * @returns Object with loading states and control methods
 */
export function useDelayedLoading(options: DelayedLoadingOptions = {}) {
  const { delayMs = 150, timeoutMs = 15000, onTimeout } = options;

  // State management
  // eslint-disable-next-line no-undef
  let loading = $state(false);
  // eslint-disable-next-line no-undef
  let showSpinner = $state(false);
  // eslint-disable-next-line no-undef
  let error = $state(false);

  // Timeout tracking
  let spinnerDelayTimeout: ReturnType<typeof setTimeout> | undefined;
  let loadingTimeout: ReturnType<typeof setTimeout> | undefined;

  /**
   * Clears the spinner delay timeout
   */
  const clearSpinnerDelayTimeout = () => {
    if (spinnerDelayTimeout) {
      clearTimeout(spinnerDelayTimeout);
      spinnerDelayTimeout = undefined;
    }
  };

  /**
   * Clears the loading timeout
   */
  const clearLoadingTimeout = () => {
    if (loadingTimeout) {
      clearTimeout(loadingTimeout);
      loadingTimeout = undefined;
    }
  };

  /**
   * Clears all timeouts
   */
  const clearAllTimeouts = () => {
    clearSpinnerDelayTimeout();
    clearLoadingTimeout();
  };

  /**
   * Sets the loading state and manages timeout behavior
   *
   * @param isLoading Whether to start or stop loading
   */
  const setLoading = (isLoading: boolean) => {
    if (isLoading) {
      // Start loading
      loading = true;
      error = false;
      showSpinner = false; // Don't show spinner immediately

      // Clear any existing timeouts
      clearAllTimeouts();

      // Only show spinner if loading takes longer than delayMs
      spinnerDelayTimeout = setTimeout(() => {
        if (loading) {
          showSpinner = true;
        }
      }, delayMs);

      // Set timeout for loading operation
      if (timeoutMs > 0) {
        loadingTimeout = setTimeout(() => {
          if (loading) {
            loading = false;
            showSpinner = false;
            error = true;
            clearSpinnerDelayTimeout();

            if (onTimeout) {
              onTimeout();
            }
          }
        }, timeoutMs);
      }
    } else {
      // Stop loading
      loading = false;
      showSpinner = false;
      clearAllTimeouts();
    }
  };

  /**
   * Marks the loading as failed
   */
  const setError = () => {
    loading = false;
    showSpinner = false;
    error = true;
    clearAllTimeouts();
  };

  /**
   * Resets all state to initial values
   */
  const reset = () => {
    loading = false;
    showSpinner = false;
    error = false;
    clearAllTimeouts();
  };

  /**
   * Cleanup function to be called on component destroy
   */
  const cleanup = () => {
    clearAllTimeouts();
  };

  // Automatically cleanup on component destroy
  onDestroy(cleanup);

  return {
    // State (read-only)
    get loading() {
      return loading;
    },
    get showSpinner() {
      return showSpinner;
    },
    get error() {
      return error;
    },

    // Methods
    setLoading,
    setError,
    reset,
    cleanup,
  };
}

/**
 * Creates a delayed loading state manager for image loading specifically
 *
 * @param options Configuration options for delayed loading behavior
 * @returns Object with loading states and control methods
 */
export function useImageDelayedLoading(options: DelayedLoadingOptions = {}) {
  const delayedLoading = useDelayedLoading({
    delayMs: 150,
    timeoutMs: 10000, // 10 seconds for images
    ...options,
  });

  // Track failed URLs to prevent retry loops
  const failedUrls = new Set<string>();

  /**
   * Checks if a URL has previously failed
   */
  const hasUrlFailed = (url: string): boolean => {
    return failedUrls.has(url);
  };

  /**
   * Marks a URL as failed
   */
  const markUrlFailed = (url: string) => {
    failedUrls.add(url);
    delayedLoading.setError();
  };

  /**
   * Clears the failed URLs cache
   */
  const clearFailedUrls = () => {
    failedUrls.clear();
  };

  return {
    ...delayedLoading,
    hasUrlFailed,
    markUrlFailed,
    clearFailedUrls,
  };
}

/**
 * Test Timeout Constants
 *
 * Centralized timeout values for consistent test behavior across the suite.
 * These values balance test reliability with execution speed.
 */

/**
 * Time to wait for component initialization and first render
 */
export const INIT_TIMEOUT = 100;

/**
 * Time to wait for reactive state updates and DOM reconciliation
 */
export const STATE_UPDATE_TIMEOUT = 200;

/**
 * Time to wait for animations and transitions to complete
 */
export const ANIMATION_TIMEOUT = 300;

/**
 * Time to wait for async operations like API calls
 */
export const ASYNC_TIMEOUT = 500;

/**
 * Maximum time for long-running operations in tests
 */
export const MAX_TEST_TIMEOUT = 5000;

/**
 * Time to wait between rapid successive operations
 */
export const DEBOUNCE_TIMEOUT = 50;

/**
 * Helper function to wait for a specific duration
 * @param ms Duration in milliseconds
 * @returns Promise that resolves after the specified duration
 */
export function wait(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/**
 * Helper function to wait for next tick with optional additional delay
 * @param additionalDelay Optional extra delay after next tick
 * @returns Promise that resolves after next tick and optional delay
 */
export async function waitForNextTick(additionalDelay = 0): Promise<void> {
  await new Promise(resolve => setTimeout(resolve, 0));
  if (additionalDelay > 0) {
    await wait(additionalDelay);
  }
}

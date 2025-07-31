/* eslint-disable no-console */
/**
 * Lightweight logger utility for BirdNET-Go frontend
 *
 * Features:
 * - Environment-aware (logs only in development by default)
 * - Category-based logging for filtering
 * - Sentry-ready structure for future integration
 * - Zero configuration required
 * - TypeScript support
 *
 * Usage:
 * ```typescript
 * import { getLogger } from '$lib/utils/logger';
 *
 * const logger = getLogger('notifications');
 * logger.debug('Connection established');
 * logger.error('Failed to connect', error, { userId: '123' });
 * ```
 */

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface LogContext {
  component?: string;
  action?: string;
  userId?: string;
  sessionId?: string;
  [key: string]: unknown;
}

export interface Logger {
  debug(...args: unknown[]): void;
  info(...args: unknown[]): void;
  warn(message: string, error?: Error | unknown, context?: LogContext, throttleKey?: string): void;
  error(message: string, error?: Error | unknown, context?: LogContext, throttleKey?: string): void;
  group(label: string): void;
  groupEnd(): void;
  time(label: string): void;
  timeEnd(label: string): void;
}

// Check if we're in development mode
const isDev = import.meta.env.DEV;

// Store for timing measurements with automatic cleanup
const timers = new Map<string, number>();
const MAX_TIMERS = 100; // Prevent memory leaks from abandoned timers

// Rate limiting for high-frequency logs
const logThrottleMap = new Map<string, number>();
const THROTTLE_INTERVAL = 1000; // 1 second

// Cleanup old timers if we exceed the limit
function cleanupTimers() {
  if (timers.size > MAX_TIMERS) {
    // Remove oldest entries (first half of entries)
    const entries = Array.from(timers.entries());
    const toRemove = entries.slice(0, Math.floor(entries.length / 2));
    toRemove.forEach(([key]) => timers.delete(key));
  }
}

// Check if a log message should be throttled
function shouldThrottle(key: string): boolean {
  const now = Date.now();
  const lastLogged = logThrottleMap.get(key);

  if (!lastLogged || now - lastLogged > THROTTLE_INTERVAL) {
    logThrottleMap.set(key, now);
    return false;
  }

  return true;
}

/**
 * Creates a logger instance for a specific category
 * @param category - The logging category (e.g., 'api', 'sse', 'auth')
 * @returns Logger instance
 */
export function getLogger(category: string): Logger {
  const prefix = `[${category}]`;

  return {
    debug(...args: unknown[]): void {
      if (isDev) {
        console.log(prefix, ...args);
      }
    },

    info(...args: unknown[]): void {
      if (isDev) {
        console.info(prefix, ...args);
      }
    },

    warn(
      message: string,
      error?: Error | unknown,
      context?: LogContext,
      throttleKey?: string
    ): void {
      // Check throttling if throttleKey provided
      if (throttleKey && shouldThrottle(`${category}:warn:${throttleKey}`)) {
        return;
      }

      // Warnings are logged in both dev and prod
      if (error instanceof Error) {
        console.warn(prefix, message, error, context);
      } else if (error) {
        console.warn(prefix, message, error, context);
      } else {
        console.warn(prefix, message, context);
      }
    },

    error(
      message: string,
      error?: Error | unknown,
      context?: LogContext,
      throttleKey?: string
    ): void {
      // Check throttling if throttleKey provided
      if (throttleKey && shouldThrottle(`${category}:error:${throttleKey}`)) {
        return;
      }
      // Errors are always logged
      const errorData = {
        message,
        category,
        timestamp: new Date().toISOString(),
        ...context,
      };

      if (error instanceof Error) {
        console.error(prefix, message, error, errorData);

        // Future: This is where Sentry integration would go
        // if (window.Sentry) {
        //   window.Sentry.captureException(error, {
        //     contexts: { logger: errorData },
        //     tags: { category },
        //   });
        // }
      } else if (error) {
        console.error(prefix, message, error, errorData);
      } else {
        console.error(prefix, message, errorData);
      }
    },

    group(label: string): void {
      if (isDev) {
        console.group(`${prefix} ${label}`);
      }
    },

    groupEnd(): void {
      if (isDev) {
        console.groupEnd();
      }
    },

    time(label: string): void {
      if (isDev) {
        const key = `${category}:${label}`;
        cleanupTimers(); // Prevent memory leaks
        timers.set(key, globalThis.performance.now());
      }
    },

    timeEnd(label: string): void {
      if (isDev) {
        const key = `${category}:${label}`;
        const start = timers.get(key);
        if (start !== undefined) {
          const duration = globalThis.performance.now() - start;
          console.log(`${prefix} ${label}: ${duration.toFixed(2)}ms`);
          timers.delete(key);
        }
      }
    },
  };
}

/**
 * Default logger for general use
 */
export const logger = getLogger('app');

// Expose logger utilities in development for debugging
if (isDev && typeof globalThis.window !== 'undefined') {
  interface WindowWithLogger extends Window {
    __birdnetLogger: {
      getLogger: typeof getLogger;
      loggers: typeof loggers;
      logger: typeof logger;
    };
  }
  (globalThis.window as WindowWithLogger).__birdnetLogger = { getLogger, loggers, logger };
}

/**
 * Common logger categories
 */
export const loggers = {
  api: getLogger('api'),
  auth: getLogger('auth'),
  sse: getLogger('sse'),
  audio: getLogger('audio'),
  ui: getLogger('ui'),
  settings: getLogger('settings'),
  analytics: getLogger('analytics'),
  performance: getLogger('performance'),
} as const;
